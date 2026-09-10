// Package rfxgithub supplies the GitHub pack's bounded, argv-only operations.
// The host owns approval; this package never accepts approval in model arguments.
package rfxgithub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion = 1
	PatchLimit    = 12000
	InputLimit    = 32768
	OutputLimit   = 60000
	commandLimit  = 256 * 1024
)

type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Effect    string `json:"effect,omitempty"`
	RetrySafe bool   `json:"retry_safe"`
}

type Result struct {
	SchemaVersion int      `json:"schema_version"`
	OK            bool     `json:"ok"`
	Talent        string   `json:"talent"`
	Repository    string   `json:"repository,omitempty"`
	Data          any      `json:"data,omitempty"`
	Error         *Failure `json:"error,omitempty"`
	Truncated     bool     `json:"truncated"`
}

type File struct {
	Path         string `json:"path"`
	OriginalPath string `json:"original_path,omitempty"`
	Index        string `json:"index"`
	Worktree     string `json:"worktree"`
	Untracked    bool   `json:"untracked,omitempty"`
}

type Identity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}
type Remote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type Commit struct {
	OID     string `json:"oid"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Time    string `json:"time"`
}
type Status struct {
	Repository bool     `json:"repository"`
	Branch     string   `json:"branch"`
	Head       string   `json:"head"`
	Unborn     bool     `json:"unborn"`
	Upstream   string   `json:"upstream,omitempty"`
	Ahead      int      `json:"ahead"`
	Behind     int      `json:"behind"`
	Files      []File   `json:"files"`
	TotalFiles int      `json:"total_files"`
	Identity   Identity `json:"identity"`
	Remotes    []Remote `json:"remotes"`
	LastCommit *Commit  `json:"last_commit,omitempty"`
}
type Diff struct {
	Scope                    string   `json:"scope"`
	Patch                    string   `json:"patch"`
	Untracked                []string `json:"untracked"`
	UntrackedContentIncluded bool     `json:"untracked_content_included"`
}
type History struct {
	Commits []Commit `json:"commits"`
}

type operationError struct {
	code, message, effect string
	retrySafe             bool
}

func (e *operationError) Error() string { return e.message }
func fail(code, message string) error {
	return &operationError{code: code, message: message, retrySafe: true}
}

type repo struct{ dir string }

// Run executes one declared talent. approved must come from the host's protected
// approval channel, not from arguments or an ambient user environment variable.
func Run(ctx context.Context, workspace, talent string, args json.RawMessage, approved bool) Result {
	result := Result{SchemaVersion: SchemaVersion, Talent: talent}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if len(args) > InputLimit {
		return failed(result, fail("invalid_arguments", "arguments exceed 32768 bytes"))
	}
	if talent == "gh-switch" {
		return failed(result, fail("operator_only", "GitHub account switching is operator-only; use the host account settings outside a model run"))
	}
	if (talent == "git-push" || talent == "git-publish") && !approved {
		return failed(result, fail("human_approval_required", "an explicit host-owned human approval is required; JSON confirmation arguments are not accepted"))
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	dir, err := filepath.Abs(workspace)
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		return failed(result, fail("invalid_workspace", "workspace must be an existing directory"))
	}
	r := repo{dir: dir}
	if talent == "git-init" {
		var p struct{}
		if err = decode(args, &p); err == nil {
			result.Data, err = r.init(ctx)
		}
		result.Repository = dir
		if err != nil {
			return failed(result, err)
		}
		result.OK = true
		return result
	}
	if err = r.check(ctx); err != nil {
		var op *operationError
		if talent == "git-status" && errors.As(err, &op) && op.code == "not_repository" {
			var p struct{}
			if parseErr := decode(args, &p); parseErr != nil {
				return failed(result, parseErr)
			}
			result.OK = true
			result.Repository = dir
			result.Data = map[string]any{"repository": false, "reason": "not_repository", "instruction": "This workspace is not a Git repository. Initialize only after an explicit user request."}
			return result
		}
		return failed(result, err)
	}
	result.Repository = dir
	switch talent {
	case "git-status":
		var p struct{}
		if err = decode(args, &p); err == nil {
			result.Data, result.Truncated, err = r.status(ctx)
		}
	case "git-add":
		var p struct {
			Paths []string `json:"paths"`
		}
		if err = decode(args, &p); err == nil {
			result.Data, err = r.stage(ctx, p.Paths)
		}
	case "git-diff":
		var p struct {
			Scope string `json:"scope"`
		}
		if err = decode(args, &p); err == nil {
			result.Data, result.Truncated, err = r.diff(ctx, p.Scope)
		}
	case "git-log":
		var p struct {
			Count int `json:"count"`
		}
		if err = decode(args, &p); err == nil {
			if p.Count < 1 || p.Count > 50 {
				err = fail("invalid_arguments", "count must be between 1 and 50")
			} else {
				result.Data, err = r.history(ctx, p.Count)
			}
		}
	case "git-commit":
		var p struct {
			Message string `json:"message"`
		}
		if err = decode(args, &p); err == nil {
			result.Data, err = r.commit(ctx, p.Message)
		}
	case "git-profile":
		var p struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err = decode(args, &p); err == nil {
			result.Data, err = r.profile(ctx, p.Name, p.Email)
		}
	case "git-suggest":
		var p struct{}
		if err = decode(args, &p); err == nil {
			var status Status
			var diff Diff
			var a, b bool
			status, a, err = r.status(ctx)
			if err == nil {
				diff, b, err = r.diff(ctx, "staged")
			}
			if err == nil {
				result.Data = struct {
					Status      Status `json:"status"`
					Diff        Diff   `json:"diff"`
					ModelCalled bool   `json:"model_called"`
					Instruction string `json:"instruction"`
				}{status, diff, false, "Use the staged patch to suggest a commit message in the current host run; no model was called by this talent."}
				result.Truncated = a || b
			}
		}
	case "gh-accounts":
		var p struct{}
		if err = decode(args, &p); err == nil {
			result.Data, err = r.account(ctx)
		}
	case "git-push":
		var p struct {
			Remote       string `json:"remote"`
			Branch       string `json:"branch"`
			ExpectedHead string `json:"expected_head"`
		}
		if err = decode(args, &p); err == nil {
			result.Data, err = r.push(ctx, p.Remote, p.Branch, p.ExpectedHead)
		}
	case "git-publish":
		var p struct {
			Name         string `json:"name"`
			Visibility   string `json:"visibility"`
			ExpectedHead string `json:"expected_head"`
		}
		if err = decode(args, &p); err == nil {
			result.Data, err = r.publish(ctx, p.Name, p.Visibility, p.ExpectedHead)
		}
	default:
		err = fail("unknown_talent", "unknown GitHub talent")
	}
	if err != nil {
		return failed(result, err)
	}
	result.OK = true
	// A complete JSON object is always emitted. Never silently byte-truncate JSON.
	encoded, e := json.Marshal(result)
	if e != nil || len(encoded) > OutputLimit {
		return failed(Result{SchemaVersion: SchemaVersion, Talent: talent, Repository: dir}, fail("output_limit", "structured result exceeds 60000 bytes; narrow the request"))
	}
	return result
}

func failed(r Result, err error) Result {
	r.OK = false
	var e *operationError
	if errors.As(err, &e) {
		r.Error = &Failure{Code: e.code, Message: clip(e.message, 2000), Effect: e.effect, RetrySafe: e.retrySafe}
	} else {
		r.Error = &Failure{Code: "operation_failed", Message: clip(err.Error(), 2000), RetrySafe: false}
	}
	return r
}

func decode(raw []byte, dest any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return fail("invalid_arguments", "arguments must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dest); err != nil {
		return fail("invalid_arguments", err.Error())
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fail("invalid_arguments", "expected exactly one JSON object")
	}
	return nil
}

// DecodeAndRun is the standalone JSON stdin/stdout interface, shared with tests.
func DecodeAndRun(ctx context.Context, workspace, talent string, input io.Reader, output io.Writer, approved bool) int {
	args, err := io.ReadAll(io.LimitReader(input, InputLimit+1))
	var result Result
	if err != nil {
		result = failed(Result{SchemaVersion: SchemaVersion, Talent: talent}, fail("invalid_arguments", "cannot read JSON input"))
	} else {
		result = Run(ctx, workspace, talent, args, approved)
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return 1
	}
	if !result.OK {
		return 1
	}
	return 0
}

type boundedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	available := b.limit - len(b.data)
	if len(p) > available {
		b.truncated = true
		p = p[:available]
	}
	b.data = append(b.data, p...)
	return n, nil
}

type commandResult struct {
	out       string
	stderr    string
	truncated bool
	exit      int
}

func command(ctx context.Context, dir, program, input string, limit int, args ...string) (commandResult, error) {
	c := exec.CommandContext(ctx, program, args...)
	c.Dir = dir
	// Ignore inherited GIT_* redirect/config injection, credential tokens, and
	// global Git commands. Identity is deliberately repository-local.
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "LANG=C.UTF-8", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_COUNT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_LITERAL_PATHSPECS=1", "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1", "GH_PAGER=cat"}
	c.Stdin = strings.NewReader(input)
	stdout, stderr := &boundedBuffer{limit: limit}, &boundedBuffer{limit: 4000}
	c.Stdout, c.Stderr = stdout, stderr
	c.WaitDelay = 2 * time.Second
	err := c.Run()
	res := commandResult{out: string(stdout.data), stderr: string(stderr.data), truncated: stdout.truncated}
	if err != nil {
		res.exit = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			res.exit = exit.ExitCode()
		}
	}
	return res, err
}

func (r repo) gitRaw(ctx context.Context, input string, limit int, args ...string) (commandResult, error) {
	base := []string{"--no-pager", "-c", "color.ui=false", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.attributesFile=/dev/null", "-c", "commit.gpgSign=false"}
	return command(ctx, r.dir, "git", input, limit, append(base, args...)...)
}
func (r repo) git(ctx context.Context, args ...string) (string, error) {
	res, err := r.gitRaw(ctx, "", commandLimit, args...)
	if err != nil {
		return "", gitError(res, err)
	}
	if res.truncated {
		return "", fail("output_limit", "Git output exceeds 262144 bytes; narrow the repository or request")
	}
	return res.out, nil
}
func gitError(res commandResult, err error) error {
	message := strings.TrimSpace(res.stderr)
	if message == "" {
		message = err.Error()
	}
	return &operationError{code: "git_failed", message: clip(redact(message), 2000), effect: "inspect_repository_before_retry", retrySafe: false}
}

var secretURL = regexp.MustCompile(`(?i)(https?://)[^\s/@]+(?::[^\s/@]*)?@`)

func redact(s string) string { return secretURL.ReplaceAllString(s, "${1}[redacted]@") }
func clip(s string, max int) string {
	if len(s) > max {
		s = s[:max]
	}
	return strings.ToValidUTF8(s, "�")
}

func (r repo) check(ctx context.Context) error {
	root, err := r.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return fail("not_repository", "workspace is not a Git working-tree repository; initialize only after an explicit user request")
		}
		return err
	}
	root = strings.TrimSuffix(root, "\n")
	root, err = filepath.EvalSymlinks(root)
	if err != nil || root != r.dir {
		return fail("workspace_not_repo_root", "workspace must equal the repository root; parent repositories are not implicitly in scope")
	}
	info, err := os.Lstat(filepath.Join(r.dir, ".git"))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail("external_git_directory", "linked worktrees and external/symlinked Git metadata require a separate host capability; this pack accepts a local .git directory")
	}
	return nil
}

func (r repo) init(ctx context.Context) (any, error) {
	if _, err := r.git(ctx, "rev-parse", "--show-toplevel"); err == nil {
		return nil, fail("repository_exists", "workspace is already inside a repository; initialization was not performed")
	}
	if _, err := os.Lstat(filepath.Join(r.dir, ".git")); err == nil || !os.IsNotExist(err) {
		return nil, fail("repository_exists", "Git metadata already exists; initialization was not performed")
	}
	if _, err := r.git(ctx, "init", "-b", "main"); err != nil {
		return nil, err
	}
	return map[string]any{"branch": "main", "initialized": true}, nil
}

func (r repo) head(ctx context.Context) (string, bool, error) {
	res, err := r.gitRaw(ctx, "", 128, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil && res.exit == 1 {
		return "", true, nil
	}
	if err != nil {
		return "", false, gitError(res, err)
	}
	return strings.TrimSpace(res.out), false, nil
}

func (r repo) config(ctx context.Context, key string) (string, error) {
	res, err := r.gitRaw(ctx, "", 4096, "config", "--local", "--get", key)
	if err != nil && res.exit == 1 {
		return "", nil
	}
	if err != nil {
		return "", gitError(res, err)
	}
	if res.truncated {
		return "", fail("output_limit", "repository configuration value exceeds 4096 bytes")
	}
	return strings.TrimSuffix(res.out, "\n"), nil
}

func (r repo) status(ctx context.Context) (Status, bool, error) {
	s := Status{Repository: true, Files: []File{}, Remotes: []Remote{}}
	raw, err := r.git(ctx, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all")
	if err != nil {
		return s, false, err
	}
	records := strings.Split(raw, "\x00")
	for i := 0; i < len(records); i++ {
		line := records[i]
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			s.Head = strings.TrimPrefix(line, "# branch.oid ")
			if s.Head == "(initial)" {
				s.Head = ""
				s.Unborn = true
			}
		case strings.HasPrefix(line, "# branch.head "):
			s.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			_, _ = fmt.Sscanf(line, "# branch.ab +%d -%d", &s.Ahead, &s.Behind)
		case strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 ") || strings.HasPrefix(line, "u "):
			n := 9
			if line[0] == '2' {
				n = 10
			}
			if line[0] == 'u' {
				n = 11
			}
			fields := strings.SplitN(line, " ", n)
			if len(fields) != n || len(fields[1]) != 2 {
				return s, false, fail("invalid_git_output", "cannot decode porcelain-v2 status record")
			}
			f := File{Path: fields[n-1], Index: fields[1][:1], Worktree: fields[1][1:]}
			if line[0] == '2' {
				i++
				if i >= len(records) {
					return s, false, fail("invalid_git_output", "missing original rename path")
				}
				f.OriginalPath = records[i]
			}
			s.Files = append(s.Files, f)
		case strings.HasPrefix(line, "? "):
			s.Files = append(s.Files, File{Path: line[2:], Index: "?", Worktree: "?", Untracked: true})
		case line == "", strings.HasPrefix(line, "# "), strings.HasPrefix(line, "! "):
		default:
			return s, false, fail("invalid_git_output", "unknown porcelain-v2 status record")
		}
	}
	s.TotalFiles = len(s.Files)
	truncated := len(s.Files) > 100
	if truncated {
		s.Files = s.Files[:100]
	}
	// Byte-budget entries without changing the exact path of any retained file.
	for len(s.Files) > 0 {
		b, _ := json.Marshal(s.Files)
		if len(b) <= 20000 {
			break
		}
		s.Files = s.Files[:len(s.Files)-1]
		truncated = true
	}
	s.Identity.Name, err = r.config(ctx, "user.name")
	if err != nil {
		return s, truncated, err
	}
	s.Identity.Email, err = r.config(ctx, "user.email")
	if err != nil {
		return s, truncated, err
	}
	remotes, err := r.git(ctx, "remote")
	if err != nil {
		return s, truncated, err
	}
	for _, name := range strings.Split(strings.TrimSpace(remotes), "\n") {
		if name == "" {
			continue
		}
		if len(s.Remotes) == 10 {
			truncated = true
			break
		}
		remote, e := r.git(ctx, "remote", "get-url", "--", name)
		if e != nil {
			return s, truncated, e
		}
		s.Remotes = append(s.Remotes, Remote{Name: clip(name, 128), URL: clip(redact(strings.TrimSpace(remote)), 1024)})
	}
	if !s.Unborn {
		h, e := r.history(ctx, 1)
		if e != nil {
			return s, truncated, e
		}
		if len(h.Commits) == 1 {
			s.LastCommit = &h.Commits[0]
		}
	}
	return s, truncated, nil
}

func (r repo) history(ctx context.Context, count int) (History, error) {
	h := History{Commits: []Commit{}}
	_, unborn, err := r.head(ctx)
	if err != nil || unborn {
		return h, err
	}
	raw, err := r.git(ctx, "log", "-z", "--format=%H%x00%s%x00%an%x00%aI", "-n", strconv.Itoa(count))
	if err != nil {
		return h, err
	}
	fields := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	if len(fields)%4 != 0 {
		return h, fail("invalid_git_output", "cannot decode Git history")
	}
	for i := 0; i < len(fields); i += 4 {
		h.Commits = append(h.Commits, Commit{OID: fields[i], Subject: clip(fields[i+1], 512), Author: clip(fields[i+2], 256), Time: fields[i+3]})
	}
	return h, nil
}

func (r repo) diff(ctx context.Context, scope string) (Diff, bool, error) {
	if scope == "" {
		scope = "all"
	}
	d := Diff{Scope: scope, Untracked: []string{}}
	if scope != "all" && scope != "staged" && scope != "worktree" {
		return d, false, fail("invalid_arguments", "scope must be all, staged, or worktree")
	}
	_, unborn, err := r.head(ctx)
	if err != nil {
		return d, false, err
	}
	sets := [][]string{{"diff", "--no-ext-diff", "--no-textconv", "--no-color"}}
	if scope == "staged" {
		sets[0] = append(sets[0], "--cached")
	} else if scope == "all" {
		if unborn {
			sets = append(sets, append(append([]string{}, sets[0]...), "--cached"))
		} else {
			sets[0] = append(sets[0], "HEAD")
		}
	}
	truncated := false
	for _, args := range sets {
		args = append(args, "--")
		res, e := r.gitRaw(ctx, "", PatchLimit-len(d.Patch), args...)
		if e != nil {
			return d, false, gitError(res, e)
		}
		d.Patch += res.out
		truncated = truncated || res.truncated
	}
	d.Patch = strings.ToValidUTF8(d.Patch, "�")
	if len(d.Patch) > PatchLimit {
		d.Patch = clip(d.Patch, PatchLimit-3)
		truncated = true
	}
	if scope != "staged" {
		raw, e := r.git(ctx, "ls-files", "--others", "--exclude-standard", "-z")
		if e != nil {
			return d, truncated, e
		}
		if raw != "" {
			d.Untracked = strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
		}
		for len(d.Untracked) > 0 {
			b, _ := json.Marshal(d.Untracked)
			if len(d.Untracked) <= 100 && len(b) <= 12000 {
				break
			}
			d.Untracked = d.Untracked[:len(d.Untracked)-1]
			truncated = true
		}
	}
	return d, truncated, nil
}

func (r repo) stage(ctx context.Context, paths []string) (any, error) {
	if len(paths) == 0 || len(paths) > 128 {
		return nil, fail("invalid_arguments", "paths must contain 1 to 128 explicit file paths; directories and stage-all are not accepted")
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if path == "" || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || strings.HasPrefix(path, "../") {
			return nil, fail("invalid_arguments", "each path must be a literal workspace-relative file path without traversal")
		}
		for _, part := range strings.Split(path, string(filepath.Separator)) {
			if part == ".git" {
				return nil, fail("invalid_arguments", "Git metadata cannot be staged")
			}
		}
		if seen[path] {
			return nil, fail("invalid_arguments", "duplicate paths are not accepted")
		}
		seen[path] = true
		if err := r.checkStageParents(path); err != nil {
			return nil, err
		}
		info, err := os.Lstat(filepath.Join(r.dir, path))
		if err == nil && info.IsDir() {
			return nil, fail("invalid_arguments", "directories are not accepted; list the specific files to stage")
		}
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fail("invalid_arguments", "cannot inspect requested file")
			}
			if _, err = r.git(ctx, "ls-files", "--error-unmatch", "--", path); err != nil {
				return nil, fail("invalid_arguments", "requested path is neither an existing file nor a tracked deletion")
			}
		} else if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return nil, fail("invalid_arguments", "only regular files, symlinks, and tracked deletions can be staged")
		}
	}
	// A clean/process filter can execute arbitrary programs during git add.
	filters, err := r.gitRaw(ctx, "", 4096, "config", "--get-regexp", `^filter\..*\.(clean|process)$`)
	if err == nil {
		return nil, fail("external_filter", "repository clean/process filters require operator review; staging was not performed")
	}
	if filters.exit != 1 {
		return nil, gitError(filters, err)
	}
	res, err := r.gitRaw(ctx, strings.Join(paths, "\x00")+"\x00", commandLimit, "add", "--pathspec-from-file=-", "--pathspec-file-nul")
	if err != nil {
		return nil, gitError(res, err)
	}
	return map[string]any{"staged_paths": paths, "scope": "explicit_files_only"}, nil
}
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// A tracked deletion may no longer have any of its former parent directories.
// Validate each existing ancestor without following symlinks, then let the
// exact literal ls-files lookup above prove the missing file is tracked.
func (r repo) checkStageParents(path string) error {
	parent := r.dir
	parts := strings.Split(filepath.Dir(path), string(filepath.Separator))
	for _, part := range parts {
		if part == "." {
			continue
		}
		parent = filepath.Join(parent, part)
		if !inside(r.dir, parent) {
			return fail("invalid_arguments", "file parent must remain inside the workspace")
		}
		info, err := os.Lstat(parent)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fail("invalid_arguments", "existing file parents must be directories inside the workspace, without symlinks")
		}
	}
	return nil
}

func (r repo) commit(ctx context.Context, message string) (any, error) {
	if strings.TrimSpace(message) == "" || len(message) > 4096 || strings.ContainsRune(message, 0) || !utf8.ValidString(message) {
		return nil, fail("invalid_arguments", "message must be nonempty UTF-8 text of at most 4096 bytes")
	}
	changed, err := r.git(ctx, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	if changed == "" {
		return nil, fail("nothing_staged", "nothing staged; review the diff and stage specific files first")
	}
	for _, key := range []string{"user.name", "user.email"} {
		value, e := r.config(ctx, key)
		if e != nil {
			return nil, e
		}
		if strings.TrimSpace(value) == "" {
			return nil, fail("identity_required", "repository-local name and email are required; use git-profile after user approval")
		}
	}
	if _, err = r.git(ctx, "commit", "--no-gpg-sign", "-m", message); err != nil {
		return nil, err
	}
	head, _, err := r.head(ctx)
	return map[string]any{"head": head, "committed": "staged_index_only", "hooks": "disabled", "signing": "disabled"}, err
}

func (r repo) profile(ctx context.Context, name, email string) (any, error) {
	for _, value := range []string{name, email} {
		if strings.TrimSpace(value) == "" || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n<>") || !utf8.ValidString(value) {
			return nil, fail("invalid_arguments", "identity fields must be nonempty single-line text without angle brackets, at most 256 bytes")
		}
	}
	if !strings.Contains(email, "@") {
		return nil, fail("invalid_arguments", "email must contain @")
	}
	if _, err := r.git(ctx, "config", "--local", "user.name", name); err != nil {
		return nil, err
	}
	if _, err := r.git(ctx, "config", "--local", "user.email", email); err != nil {
		return nil, err
	}
	return map[string]any{"identity": Identity{Name: name, Email: email}, "scope": "repository_local"}, nil
}

type Account struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	URL   string `json:"html_url"`
}

func (r repo) account(ctx context.Context) (any, error) {
	res, err := command(ctx, r.dir, "gh", "", 32768, "api", "--hostname", "github.com", "user")
	if err != nil {
		return nil, fail("github_account_unavailable", "GitHub account lookup failed; check gh authentication as the operator (credentials are not returned)")
	}
	var a Account
	if res.truncated || json.Unmarshal([]byte(res.out), &a) != nil || !ownerRE.MatchString(a.Login) || a.ID <= 0 {
		return nil, fail("invalid_github_output", "GitHub did not return a valid active account")
	}
	a.URL = "https://github.com/" + a.Login
	return map[string]any{"host": "github.com", "active_account": a, "scope": "active_account_only", "switching": "operator_only"}, nil
}

var ownerRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
var remoteRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var oidRE = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

func (r repo) expected(ctx context.Context, expected string) (string, error) {
	if !oidRE.MatchString(expected) {
		return "", fail("invalid_arguments", "expected_head must be the full commit OID reviewed by the user")
	}
	head, unborn, err := r.head(ctx)
	if err != nil {
		return "", err
	}
	if unborn || head != expected {
		return "", fail("head_changed", "HEAD differs from the reviewed commit; review and confirm again")
	}
	return head, nil
}

func githubURL(raw string) bool {
	if strings.HasPrefix(raw, "git@github.com:") {
		parts := strings.Split(strings.TrimPrefix(raw, "git@github.com:"), "/")
		return len(parts) == 2 && ownerRE.MatchString(parts[0]) && repoNameRE.MatchString(parts[1]) && parts[1] != "." && parts[1] != ".."
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "ssh") || u.Host != "github.com" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.User != nil {
		if u.Scheme != "ssh" || u.User.Username() != "git" {
			return false
		}
		if _, ok := u.User.Password(); ok {
			return false
		}
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	return len(parts) == 2 && ownerRE.MatchString(parts[0]) && repoNameRE.MatchString(parts[1]) && parts[1] != "." && parts[1] != ".."
}

func (r repo) push(ctx context.Context, remote, branch, expected string) (any, error) {
	if !remoteRE.MatchString(remote) || branch == "" || len(branch) > 200 || strings.HasPrefix(branch, "-") {
		return nil, fail("invalid_arguments", "an explicit remote name and destination branch are required")
	}
	head, err := r.expected(ctx, expected)
	if err != nil {
		return nil, err
	}
	if _, err = r.git(ctx, "check-ref-format", "--branch", branch); err != nil {
		return nil, fail("invalid_arguments", "invalid destination branch")
	}
	urls, err := r.git(ctx, "remote", "get-url", "--push", "--all", "--", remote)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSuffix(urls, "\n")
	if !githubURL(target) {
		return nil, fail("unsupported_remote", "push requires exactly one credential-free github.com HTTPS/SSH destination")
	}
	res, err := r.gitRaw(ctx, "", 8192, "push", "--porcelain", "--", remote, head+":refs/heads/"+branch)
	if err != nil {
		return nil, &operationError{code: "push_failed", message: "GitHub push failed; inspect the remote before retrying: " + clip(redact(res.stderr), 1000), effect: "remote_state_unknown", retrySafe: false}
	}
	return map[string]any{"remote": remote, "destination": target, "branch": branch, "head": head, "force": false, "receipt": clip(res.out, 8192)}, nil
}

func (r repo) publish(ctx context.Context, name, visibility, expected string) (any, error) {
	parts := strings.Split(name, "/")
	if len(parts) != 2 || !ownerRE.MatchString(parts[0]) || !repoNameRE.MatchString(parts[1]) || parts[1] == "." || parts[1] == ".." || (visibility != "private" && visibility != "public") {
		return nil, fail("invalid_arguments", "name must be explicit OWNER/REPOSITORY and visibility private or public")
	}
	head, err := r.expected(ctx, expected)
	if err != nil {
		return nil, err
	}
	remote, err := r.config(ctx, "remote.origin.url")
	if err != nil {
		return nil, err
	}
	if remote != "" {
		return nil, fail("origin_exists", "origin already exists; publishing did not run")
	}
	res, err := command(ctx, r.dir, "gh", "", 8192, "repo", "create", name, "--"+visibility, "--source=.", "--remote=origin")
	if err != nil {
		return nil, &operationError{code: "publish_failed", message: "GitHub repository creation failed; inspect GitHub and local remotes before retrying (credentials are not returned)", effect: "repository_creation_unknown", retrySafe: false}
	}
	return map[string]any{"name": name, "visibility": visibility, "head": head, "created": true, "pushed": false, "next": "Review the destination and explicitly approve git-push to upload commits", "receipt": clip(redact(res.out), 8192)}, nil
}
