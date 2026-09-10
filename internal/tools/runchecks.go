package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	runChecksStreamLimit       = 128 << 10
	runChecksMaxScriptArgs     = 32
	runChecksMaxScriptArgBytes = 1024
)

// RunChecks executes a fixed, locally installed runner in the native read-only
// sandbox. A receipt records observed results, never a guessed assertion count.
type RunChecks struct {
	j      jail
	lookup func(string) (string, error)
	run    func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult
}

func NewRunChecks(workspace string) *RunChecks {
	j := newJail(workspace)
	if canonical, err := filepath.EvalSymlinks(j.root); err == nil {
		j.root = canonical
	}
	return &RunChecks{j: j, lookup: exec.LookPath, run: runNativeProcess}
}

func (*RunChecks) Name() string { return "run_checks" }

func (*RunChecks) Description() string {
	return "Run existing Node assertion scripts, Node test TAP, or Go test JSON with fixed runtime arguments, read-only files and no network. Supply relative check paths; Go paths are existing local package directories. For a Node script test group, use script_args (e.g. [\"skylight\"]); these are literal script arguments after the single check path, never runtime flags or shell commands. Returns actual pass/fail/unverified, parsed tests when available, bounded raw output, source SHA-256 versions and an exclusive .devcheck receipt. No installs or arbitrary runtime commands/flags. Node scripts verify exit status only; they do not expose assertion counts. Pass prior_receipt explicitly to compare the same check identity (including script_args); altered protected checks invalidate verification. source_paths adds non-code inputs to the automatic local source snapshot."
}

func (*RunChecks) Schema() map[string]any {
	pathArray := func(description string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 1024}, "maxItems": 32, "description": description}
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"runner":          map[string]any{"type": "string", "enum": []string{"node_script", "node_test", "go_test"}},
			"paths":           pathArray("Existing workspace-relative check files; exactly one for node_script. Existing local package directories for go_test; recursive patterns are not accepted."),
			"script_args":     map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1, "maxLength": runChecksMaxScriptArgBytes}, "maxItems": runChecksMaxScriptArgs, "description": "node_script only: literal script arguments appended after its path, e.g. [\"skylight\"]. At most 32 nonempty UTF-8 strings, each at most 1024 bytes and without NUL. Cannot set Node runtime flags; arguments are part of the check identity."},
			"source_paths":    pathArray("Additional input files to hash before and after the run; local code, manifests and lockfiles are already recorded."),
			"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "default": 30},
			"prior_receipt":   map[string]any{"type": "string", "maxLength": 1024, "description": "Explicit prior .devcheck/run-checks/<id>.json receipt from the same workspace and check identity."},
		},
		"required": []string{"runner", "paths"},
	}
}

type runChecksArgs struct {
	Runner         string              `json:"runner"`
	Paths          []string            `json:"paths"`
	ScriptArgs     runChecksScriptArgs `json:"script_args,omitempty"`
	SourcePaths    []string            `json:"source_paths,omitempty"`
	TimeoutSeconds int                 `json:"timeout_seconds,omitempty"`
	PriorReceipt   string              `json:"prior_receipt,omitempty"`
}

type runChecksScriptArgs []string

func (a *runChecksScriptArgs) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("script_args must be an array of strings")
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	*a = values
	return nil
}

type runChecksCommand struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	CWD        string   `json:"cwd"`
	ReadOnly   bool     `json:"read_only"`
	Network    bool     `json:"network"`
}

type runChecksVersion struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Protected bool   `json:"protected_check"`
}

type runChecksTest struct {
	Name     string          `json:"name"`
	Package  string          `json:"package,omitempty"`
	Status   string          `json:"status"`
	Location string          `json:"location,omitempty"`
	Expected json.RawMessage `json:"expected,omitempty"`
	Actual   json.RawMessage `json:"actual,omitempty"`
	Evidence string          `json:"evidence,omitempty"`
}

type runChecksComparison struct {
	PriorReceipt  string `json:"prior_receipt"`
	Comparable    bool   `json:"comparable"`
	PriorStatus   string `json:"prior_status,omitempty"`
	CurrentStatus string `json:"current_status"`
	Transition    string `json:"transition,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type runChecksReceipt struct {
	Version           int                  `json:"version"`
	ReceiptPath       string               `json:"receipt_path"`
	Identity          string               `json:"check_identity_sha256"`
	InputSHA256       string               `json:"input_sha256"`
	Runner            string               `json:"runner"`
	Status            string               `json:"status"`
	VerificationScope string               `json:"verification_scope"`
	Command           runChecksCommand     `json:"command"`
	TimeoutSeconds    int                  `json:"timeout_seconds"`
	StartedAt         string               `json:"started_at"`
	ElapsedMS         int64                `json:"elapsed_ms"`
	ExitCode          int                  `json:"exit_code"`
	TimedOut          bool                 `json:"timed_out"`
	StdoutTruncated   bool                 `json:"stdout_truncated"`
	StderrTruncated   bool                 `json:"stderr_truncated"`
	Stdout            string               `json:"stdout"`
	Stderr            string               `json:"stderr"`
	Tests             []runChecksTest      `json:"tests"`
	Before            []runChecksVersion   `json:"sources_before"`
	After             []runChecksVersion   `json:"sources_after"`
	SnapshotScope     string               `json:"snapshot_scope"`
	Reasons           []string             `json:"reasons,omitempty"`
	Comparison        *runChecksComparison `json:"comparison,omitempty"`
}

func (t *RunChecks) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if len(raw) > 64<<10 {
		return "", fmt.Errorf("run_checks arguments exceed 64 KiB")
	}
	if !utf8.Valid(raw) {
		return "", fmt.Errorf("run_checks arguments must be valid UTF-8")
	}
	a := runChecksArgs{TimeoutSeconds: 30}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return "", fmt.Errorf("bad run_checks arguments: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return "", fmt.Errorf("run_checks requires one JSON object")
	}
	if a.TimeoutSeconds < 1 || a.TimeoutSeconds > 120 {
		return "", fmt.Errorf("timeout_seconds must be 1..120")
	}
	if len(a.Paths) == 0 || len(a.Paths) > 32 || len(a.SourcePaths) > 32 {
		return "", fmt.Errorf("paths requires 1..32 entries; source_paths allows at most 32")
	}
	if a.Runner != "node_script" && a.Runner != "node_test" && a.Runner != "go_test" {
		return "", fmt.Errorf("runner must be node_script, node_test, or go_test")
	}
	if a.Runner == "node_script" && len(a.Paths) != 1 {
		return "", fmt.Errorf("node_script requires exactly one script; select test groups with script_args, e.g. [\"skylight\"]")
	}
	if a.ScriptArgs != nil && a.Runner != "node_script" {
		return "", fmt.Errorf("script_args is supported only for node_script")
	}
	if len(a.ScriptArgs) > runChecksMaxScriptArgs {
		return "", fmt.Errorf("script_args allows at most %d entries", runChecksMaxScriptArgs)
	}
	for _, arg := range a.ScriptArgs {
		if arg == "" || len(arg) > runChecksMaxScriptArgBytes || !utf8.ValidString(arg) || strings.ContainsRune(arg, '\x00') {
			return "", fmt.Errorf("script_args must contain nonempty UTF-8 strings up to %d bytes without NUL", runChecksMaxScriptArgBytes)
		}
	}
	for i, p := range a.Paths {
		clean, err := t.checkPath(p, a.Runner == "go_test")
		if err != nil {
			return "", err
		}
		if a.Runner != "go_test" {
			switch strings.ToLower(filepath.Ext(clean)) {
			case ".js", ".mjs", ".cjs":
			default:
				return "", fmt.Errorf("Node checks require .js, .mjs or .cjs files")
			}
		}
		a.Paths[i] = clean
	}
	for i, p := range a.SourcePaths {
		clean, err := t.checkPath(p, false)
		if err != nil {
			return "", err
		}
		a.SourcePaths[i] = clean
	}
	var prior *runChecksReceipt
	if a.PriorReceipt != "" {
		var err error
		prior, err = t.readReceipt(a.PriorReceipt)
		if err != nil {
			return "", err
		}
	}
	name := "node"
	argv := []string{"--experimental-default-type=module"}
	if a.Runner == "node_test" {
		argv = []string{"--test", "--test-reporter=tap"}
	}
	if a.Runner == "go_test" {
		name = "go"
		argv = []string{"test", "-json", "-count=1", "-mod=readonly"}
	}
	for _, p := range a.Paths {
		argv = append(argv, "./"+filepath.ToSlash(p))
	}
	argv = append(argv, a.ScriptArgs...)
	executable, lookupErr := t.lookup(name)
	if lookupErr != nil {
		executable = name
	}
	r := runChecksReceipt{Version: 1, Runner: a.Runner, Status: "unverified", VerificationScope: "runner_reported_tests", TimeoutSeconds: a.TimeoutSeconds, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), ExitCode: -1, Tests: []runChecksTest{}, Before: []runChecksVersion{}, After: []runChecksVersion{}, Command: runChecksCommand{Executable: executable, Args: argv, CWD: t.j.root, ReadOnly: true, Network: false}, SnapshotScope: "Local JS/TS/Go/JSON/HTML/CSS/WASM source and manifest files plus explicit source_paths; excludes .git, node_modules, vendor, .devcheck, test-evidence. Imported dependencies are not recursively inventoried."}
	if a.Runner == "node_script" {
		r.VerificationScope = "script_exit_status_only_assertion_count_unknown"
	}
	identity, _ := json.Marshal(struct {
		Runner      string
		Paths       []string
		SourcePaths []string
		Command     runChecksCommand
		Timeout     int
	}{a.Runner, a.Paths, a.SourcePaths, r.Command, a.TimeoutSeconds})
	r.Identity = runChecksSHA(identity)
	input, _ := json.Marshal(a)
	r.InputSHA256 = runChecksSHA(input)
	before, snapshotErr := t.snapshot(a)
	if snapshotErr == nil {
		r.Before = before
	}
	if lookupErr != nil {
		r.Reasons = append(r.Reasons, "required runtime unavailable: "+lookupErr.Error())
	} else if snapshotErr != nil {
		r.Reasons = append(r.Reasons, "source snapshot failed; check not executed: "+snapshotErr.Error())
	} else {
		p := t.run(ctx, t.j.root, executable, argv, nil, time.Duration(a.TimeoutSeconds)*time.Second, true)
		r.Stdout, r.StdoutTruncated = boundRunChecksOutput(p.Stdout, p.StdoutTruncated)
		r.Stderr, r.StderrTruncated = boundRunChecksOutput(p.Stderr, p.StderrTruncated)
		r.ExitCode, r.ElapsedMS, r.TimedOut = p.ExitCode, p.Elapsed.Milliseconds(), p.TimedOut
		complete := a.Runner == "node_script"
		if a.Runner == "node_test" {
			r.Tests, complete = parseRunChecksTAP(r.Stdout)
		}
		if a.Runner == "go_test" {
			r.Tests, complete = parseRunChecksGo(r.Stdout)
		}
		switch {
		case strings.HasPrefix(strings.TrimSpace(r.Stderr), "bwrap:"):
			r.Reasons = append(r.Reasons, "read-only runner sandbox could not start")
		case p.TimedOut || ctx.Err() != nil:
			r.Reasons = append(r.Reasons, "check execution timed out or was canceled")
		case r.StdoutTruncated || r.StderrTruncated:
			r.Reasons = append(r.Reasons, "output exceeded the 128 KiB per-stream evidence bound")
		case p.ExitCode < 0 || (p.Err != nil && p.ExitCode == 0):
			r.Reasons = append(r.Reasons, "runner did not complete")
		case p.ExitCode != 0:
			r.Status = "fail"
		case !complete:
			r.Reasons = append(r.Reasons, "no complete named test results were parsed")
		default:
			r.Status = "pass"
			for _, test := range r.Tests {
				if test.Status == "fail" {
					r.Status = "fail"
				}
			}
		}
		if p.Err != nil {
			r.Reasons = append(r.Reasons, p.Err.Error())
		}
	}
	if after, err := t.snapshot(a); err != nil {
		r.Status = "unverified"
		r.Reasons = append(r.Reasons, "source snapshot after execution failed: "+err.Error())
	} else {
		r.After = after
		if snapshotErr == nil && !sameRunChecksVersions(r.Before, r.After, false) {
			r.Status = "unverified"
			r.Reasons = append(r.Reasons, "recorded source or protected check changed during execution")
		}
	}
	if prior != nil {
		compareRunChecks(&r, prior)
	}
	return t.writeReceipt(&r)
}

func boundRunChecksOutput(s string, truncated bool) (string, bool) {
	if len(s) > runChecksStreamLimit {
		return s[:runChecksStreamLimit], true
	}
	return s, truncated
}

func runChecksSHA(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func (t *RunChecks) checkPath(p string, directory bool) (string, error) {
	if p == "" || len(p) > 1024 || filepath.IsAbs(p) || strings.ContainsAny(p, "\x00\r\n") {
		return "", fmt.Errorf("check paths must be nonempty workspace-relative paths up to 1024 bytes")
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %q", p)
	}
	full, err := t.j.resolve(clean)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("check input %q: %w", p, err)
	}
	if directory && !info.IsDir() {
		return "", fmt.Errorf("Go check path %q must be a package directory", p)
	}
	if !directory && !info.Mode().IsRegular() {
		return "", fmt.Errorf("check input %q must be a regular file", p)
	}
	return clean, nil
}

func runChecksExcluded(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".devcheck", "test-evidence":
		return true
	}
	return false
}

func runChecksSource(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".go", ".json", ".html", ".css", ".wasm", ".mod", ".sum":
		return true
	}
	switch name {
	case "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb":
		return true
	}
	return false
}

func (t *RunChecks) snapshot(a runChecksArgs) ([]runChecksVersion, error) {
	paths := map[string]bool{}
	for _, p := range a.SourcePaths {
		paths[p] = true
	}
	if a.Runner != "go_test" {
		for _, p := range a.Paths {
			paths[p] = true
		}
	}
	visited := 0
	err := filepath.WalkDir(t.j.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		visited++
		if visited > 16384 {
			return fmt.Errorf("source snapshot traversal exceeds 16384 entries")
		}
		if path != t.j.root && entry.IsDir() && runChecksExcluded(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !runChecksSource(entry.Name()) {
			return nil
		}
		rel, err := filepath.Rel(t.j.root, path)
		if err != nil {
			return err
		}
		paths[rel] = true
		if len(paths) > 4096 {
			return fmt.Errorf("source snapshot exceeds 4096 files")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(t.j.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	names := make([]string, 0, len(paths))
	for p := range paths {
		names = append(names, p)
	}
	sort.Strings(names)
	out := make([]runChecksVersion, 0, len(names))
	total := 0
	for _, p := range names {
		if _, err := t.checkPath(p, false); err != nil {
			return nil, err
		}
		f, err := root.Open(p)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, fmt.Errorf("source %q is not a readable regular file", p)
		}
		b, err := io.ReadAll(io.LimitReader(f, 8<<20+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		total += len(b)
		if len(b) > 8<<20 || total > 64<<20 {
			return nil, fmt.Errorf("source snapshot exceeds 8 MiB per file or 64 MiB total")
		}
		protected := false
		if a.Runner == "go_test" && strings.HasSuffix(p, "_test.go") {
			protected = true
		}
		if a.Runner != "go_test" {
			for _, check := range a.Paths {
				if p == check {
					protected = true
				}
			}
		}
		out = append(out, runChecksVersion{Path: filepath.ToSlash(p), SHA256: runChecksSHA(b), Protected: protected})
	}
	return out, nil
}

func sameRunChecksVersions(a, b []runChecksVersion, protectedOnly bool) bool {
	collect := func(v []runChecksVersion) map[string]string {
		out := map[string]string{}
		for _, f := range v {
			if !protectedOnly || f.Protected {
				out[f.Path] = f.SHA256
			}
		}
		return out
	}
	aa, bb := collect(a), collect(b)
	if len(aa) != len(bb) {
		return false
	}
	for p, h := range aa {
		if bb[p] != h {
			return false
		}
	}
	return true
}

func compareRunChecks(current, prior *runChecksReceipt) {
	c := &runChecksComparison{PriorReceipt: prior.ReceiptPath, PriorStatus: prior.Status, CurrentStatus: current.Status}
	current.Comparison = c
	switch {
	case current.Identity != prior.Identity:
		c.Reason = "different runner, command, check paths, source paths, timeout or workspace; results are not comparable"
	case !sameRunChecksVersions(prior.Before, prior.After, true) || !sameRunChecksVersions(prior.After, current.Before, true):
		current.Status = "unverified"
		c.CurrentStatus = current.Status
		c.Reason = "protected checks changed; current result cannot establish a repair"
		current.Reasons = append(current.Reasons, c.Reason)
	case current.Status == "unverified" || prior.Status == "unverified":
		c.Reason = "one or both runs lack verified completion"
	default:
		c.Comparable = true
		c.Transition = prior.Status + "_to_" + current.Status
	}
}

func (t *RunChecks) readReceipt(p string) (*runChecksReceipt, error) {
	clean := filepath.ToSlash(filepath.Clean(p))
	if filepath.IsAbs(p) || len(p) > 1024 || !strings.HasPrefix(clean, ".devcheck/run-checks/") || filepath.Ext(clean) != ".json" || strings.Count(clean, "/") != 2 {
		return nil, fmt.Errorf("prior_receipt must identify a .devcheck/run-checks receipt")
	}
	root, err := os.OpenRoot(t.j.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(clean)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("prior_receipt must be a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 16<<20 {
		return nil, fmt.Errorf("prior receipt exceeds 16 MiB")
	}
	var r runChecksReceipt
	if err := json.Unmarshal(b, &r); err != nil || r.Version != 1 || r.ReceiptPath != clean || r.Command.CWD != t.j.root || len(r.Identity) != 64 {
		return nil, fmt.Errorf("invalid prior run_checks receipt")
	}
	if r.Status != "pass" && r.Status != "fail" && r.Status != "unverified" {
		return nil, fmt.Errorf("invalid prior receipt status")
	}
	return &r, nil
}

func (t *RunChecks) writeReceipt(r *runChecksReceipt) (string, error) {
	root, err := os.OpenRoot(t.j.root)
	if err != nil {
		return "", err
	}
	defer root.Close()
	for _, dir := range []string{".devcheck", ".devcheck/run-checks"} {
		if err := root.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	r.ReceiptPath = ".devcheck/run-checks/" + hex.EncodeToString(id[:]) + ".json"
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	f, err := root.OpenFile(r.ReceiptPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("exclusive evidence receipt: %w", err)
	}
	_, writeErr := f.Write(b)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	summary, err := summarizeRunChecks(r, b)
	if err != nil {
		return "", err
	}
	if r.Status != "pass" {
		return summary, fmt.Errorf("run_checks %s; inspect %s", r.Status, r.ReceiptPath)
	}
	return summary, nil
}
