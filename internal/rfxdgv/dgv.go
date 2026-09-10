// Package rfxdgv adapts the original DGV core to one-shot, workspace-scoped
// Reflex actions. It does not implement a second diagram engine or use MCP.
package rfxdgv

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	MaxInputBytes  = 256 << 10
	MaxGraphBytes  = 2 << 20
	MaxOutputBytes = 24 << 10
	maxFiles       = 10000
	maxSourceBytes = 32 << 20
)

//go:embed bridge.mjs
var bridge string

type Result map[string]any
type Client struct{ Workspace, Core string }

type request struct {
	Name         string         `json:"name"`
	Focus        string         `json:"focus,omitempty"`
	On           string         `json:"on,omitempty"`
	Limit        int            `json:"limit,omitempty"`
	ObservedHash string         `json:"observed_hash,omitempty"`
	ExpectedHash string         `json:"expected_hash,omitempty"`
	Checks       string         `json:"checks,omitempty"`
	Document     map[string]any `json:"document,omitempty"`
	Patch        map[string]any `json:"patch,omitempty"`
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ResolveCore selects a trusted installation setting, never a model argument.
func ResolveCore() (string, error) {
	if p := os.Getenv("RFX_DGV_CORE"); p != "" {
		if !filepath.IsAbs(p) {
			return "", errors.New("engine_unavailable: RFX_DGV_CORE must be absolute")
		}
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "dgv-engine", "packages", "core", "src", "index.js"), nil
}

func (c *Client) Run(ctx context.Context, action string, raw []byte) (Result, error) {
	r, err := parseRequest(action, raw)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(c.Workspace)
	if err != nil {
		return nil, fmt.Errorf("workspace_unavailable: %w", err)
	}
	defer root.Close()
	var result Result
	if action == "list" {
		result, err = list(root, r.Limit)
	} else if action == "catalog" {
		result, err = c.engine(ctx, Result{"action": action})
	} else if action == "create" || action == "update" {
		result, err = c.mutate(ctx, root, action, r)
	} else {
		result, err = c.observe(ctx, root, action, r)
	}
	if err != nil {
		return nil, err
	}
	if _, ok := result["ok"]; !ok {
		result["ok"] = true
	}
	result["action"] = action
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxOutputBytes {
		return nil, errors.New("output_limit: narrow the graph focus or reduce limit; no partial JSON emitted")
	}
	return result, nil
}

func parseRequest(action string, raw []byte) (request, error) {
	var r request
	allowed := map[string]string{"list": "limit", "catalog": "", "context": "name focus limit observed_hash", "read": "name on", "check": "name checks", "create": "name document", "update": "name expected_hash patch"}
	fields, exists := allowed[action]
	if !exists {
		return r, errors.New("invalid_action: use list, catalog, context, read, check, create or update")
	}
	if len(raw) > MaxInputBytes {
		return r, errors.New("input_limit: maximum 256 KiB")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return r, errors.New("invalid_input: expected one JSON object")
	}
	for k := range object {
		if !strings.Contains(" "+fields+" ", " "+k+" ") {
			return r, fmt.Errorf("invalid_input: unknown %s parameter %q", action, k)
		}
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("invalid_input: %w", err)
	}
	if action != "list" && action != "catalog" && !namePattern.MatchString(r.Name) {
		return r, errors.New("invalid_name: use 1–100 letters, digits, underscores or hyphens")
	}
	if r.Limit == 0 {
		r.Limit = 12
	}
	if r.Limit < 1 || r.Limit > 30 {
		return r, errors.New("invalid_input: limit must be 1–30")
	}
	if len(r.Focus) > 100 || len(r.On) > 100 {
		return r, errors.New("invalid_input: element id exceeds 100 characters")
	}
	if action == "read" && r.On == "" {
		return r, errors.New("invalid_input: on is required; use context for an overview")
	}
	if r.ObservedHash != "" && !hashPattern.MatchString(r.ObservedHash) {
		return r, errors.New("invalid_input: observed_hash must be SHA256")
	}
	if action == "update" && !hashPattern.MatchString(r.ExpectedHash) {
		return r, errors.New("invalid_input: expected_hash must be the SHA256 returned by a graph read")
	}
	if r.Checks == "" {
		r.Checks = "all"
	}
	if r.Checks != "all" && r.Checks != "lint" && r.Checks != "drift" {
		return r, errors.New("invalid_input: checks must be all, lint or drift")
	}
	if action == "create" || action == "update" {
		p := r.Document
		if action == "update" {
			p = r.Patch
		}
		if err := validateMutation(p, action == "create"); err != nil {
			return r, err
		}
	}
	return r, nil
}

func validateMutation(p map[string]any, create bool) error {
	if len(p) == 0 {
		return errors.New("invalid_patch: nonempty document or patch required")
	}
	for k := range p {
		if k != "meta" && k != "frames" && k != "nodes" && k != "edges" && !(create && k == "dgv") {
			return fmt.Errorf("invalid_patch: %s is not supported; no removal or history replacement", k)
		}
	}
	count := 0
	for _, key := range []string{"frames", "nodes", "edges"} {
		v, ok := p[key]
		if !ok {
			continue
		}
		items, ok := v.([]any)
		if !ok {
			return fmt.Errorf("invalid_patch: %s must be an array", key)
		}
		count += len(items)
		seen := map[string]bool{}
		for _, v := range items {
			item, ok := v.(map[string]any)
			if !ok {
				return errors.New("invalid_patch: each element must be an object")
			}
			id, _ := item["id"].(string)
			if id == "" || seen[id] {
				return errors.New("invalid_patch: every element needs a unique id")
			}
			seen[id] = true
			if key == "nodes" {
				var paths []any
				switch v := item["path"].(type) {
				case string:
					paths = []any{v}
				case []any:
					paths = v
				}
				for _, v := range paths {
					s, ok := v.(string)
					if !ok || !safeRelative(s) {
						return errors.New("invalid_patch: node paths must stay workspace-relative")
					}
				}
			}
		}
	}
	if count > 64 {
		return errors.New("input_limit: at most 64 graph elements per mutation; compose bounded updates")
	}
	return nil
}

func safeRelative(s string) bool {
	if s == "" || filepath.IsAbs(s) || strings.ContainsAny(s, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func readBounded(root *os.Root, name string, max int64) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not_regular: %s", name)
	}
	if info.Size() > max {
		return nil, fmt.Errorf("file_limit: %s exceeds %d bytes", name, max)
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if len(b) > int(max) {
		return nil, fmt.Errorf("file_limit: %s grew past limit", name)
	}
	return b, err
}

func load(root *os.Root, name string) (map[string]any, []byte, error) {
	b, err := readBounded(root, "dgv/"+name+".dgv.json", MaxGraphBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("graph_unavailable: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil || doc == nil {
		return nil, nil, errors.New("invalid_graph: not a JSON object")
	}
	return doc, b, nil
}

func list(root *os.Root, limit int) (Result, error) {
	dir, err := root.OpenFile("dgv", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return Result{"items": []any{}, "total": 0, "truncated": false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("graph_directory: %w", err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxFiles {
		return nil, errors.New("inventory_limit: more than 10000 graph-directory entries")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	items := []any{}
	total := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".dgv.json") {
			continue
		}
		total++
		if len(items) >= limit {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".dgv.json")
		item := Result{"name": name}
		if !namePattern.MatchString(name) || entry.Type()&os.ModeSymlink != 0 {
			item["error"] = "unsupported graph name or symlink"
			items = append(items, item)
			continue
		}
		doc, raw, err := load(root, name)
		if err != nil {
			item["error"] = "unreadable graph"
		} else {
			item["graph_sha256"] = digest(raw)
			if meta, ok := doc["meta"].(map[string]any); ok {
				item["title"] = clip(fmt.Sprint(meta["title"]), 200)
			}
			for _, k := range []string{"frames", "nodes", "edges"} {
				if a, ok := doc[k].([]any); ok {
					item[k] = len(a)
				}
			}
		}
		items = append(items, item)
	}
	return Result{"items": items, "total": total, "truncated": total > len(items)}, nil
}

var excludedDirs = map[string]bool{".git": true, "dgv": true, "node_modules": true, "dist": true, "build": true, "out": true, "target": true, ".next": true, ".svelte-kit": true, "__pycache__": true, ".venv": true, "venv": true, "vendor": true, ".cache": true, "coverage": true, ".idea": true, ".vscode": true}

func inventory(ctx context.Context, root *os.Root) ([]string, string, error) {
	// Match the upstream DGV walker's Git-aware inventory without importing its
	// MCP package. Git supplies names only; every content read remains os.Root
	// confined. No hooks, index refresh, repository mutation or network action.
	gitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	cmd.Dir = root.Name()
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_OPTIONAL_LOCKS=0"}
	var names = capBuffer{limit: 2 << 20}
	cmd.Stdout = &names
	gitErr := cmd.Run()
	if names.overflow {
		return nil, "", errors.New("inventory_limit: Git file-name output exceeds 2 MiB")
	}
	if gitCtx.Err() != nil {
		return nil, "", fmt.Errorf("inventory_timeout: %w", gitCtx.Err())
	}
	if gitErr == nil {
		files := []string{}
		seen := map[string]bool{}
		for _, path := range strings.Split(names.String(), "\x00") {
			if path == "" || seen[path] || !safeRelative(path) {
				continue
			}
			seen[path] = true
			excluded := false
			for _, part := range strings.Split(path, "/") {
				if excludedDirs[part] {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
			info, err := root.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, "", fmt.Errorf("inventory_unreadable: %w", err)
			}
			if info.Mode().IsRegular() {
				files = append(files, path)
			}
			if len(files) > maxFiles {
				return nil, "", errors.New("inventory_limit: more than 10000 Git-visible files; narrow the session workspace")
			}
		}
		sort.Strings(files)
		return files, "Git-visible regular files under session workspace, respecting ignore rules; excludes dgv, dependency, build and cache directories; symlink entries excluded", nil
	}
	files := []string{}
	visited := 0
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		visited++
		if visited > maxFiles*3 {
			return errors.New("inventory_limit: narrow the session workspace")
		}
		if entry.IsDir() {
			if excludedDirs[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		if len(files) > maxFiles {
			return errors.New("inventory_limit: more than 10000 files; narrow the session workspace")
		}
		return nil
	})
	return files, "regular files under session workspace; no symlinks; excludes dgv, .git, dependency, build and cache directories; Git unavailable or not a repository, so .gitignore is not applied", err
}

func (c *Client) observe(ctx context.Context, root *os.Root, action string, r request) (Result, error) {
	doc, raw, err := load(root, r.Name)
	if err != nil {
		return nil, err
	}
	files := []string{}
	scope := ""
	if action == "context" || action == "check" && r.Checks != "lint" {
		files, scope, err = inventory(ctx, root)
		if err != nil {
			return nil, err
		}
	}
	result, err := c.engine(ctx, Result{"action": action, "document": doc, "files": files, "focus": r.Focus, "on": r.On, "limit": r.Limit, "checks": r.Checks})
	if err != nil {
		return nil, err
	}
	result["graph_sha256"] = digest(raw)
	result["name"] = r.Name
	if action == "context" {
		result["freshness"] = fingerprint(root, digest(raw), r, result)
		delete(result, "_sources")
		delete(result, "_missing")
		delete(result, "_unmapped")
	}
	if action == "context" || action == "check" && r.Checks != "lint" {
		result["inventory_scope"] = scope
	}
	return result, nil
}

func fingerprint(root *os.Root, graph string, r request, result Result) map[string]any {
	files, _ := result["_sources"].([]any)
	missing, _ := result["_missing"].([]any)
	unmapped, _ := result["_unmapped"].([]any)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%d\x00", graph, r.Focus, r.Limit)
	issues := []any{}
	total := 0
	hashed := 0
	for _, p := range files {
		name, _ := p.(string)
		if !safeRelative(name) {
			issues = append(issues, Result{"path": name, "reason": "outside workspace"})
			continue
		}
		if total >= maxSourceBytes {
			issues = append(issues, Result{"reason": "32 MiB aggregate hash budget exhausted"})
			break
		}
		b, err := readBounded(root, name, int64(min(8<<20, maxSourceBytes-total)))
		if err != nil {
			issues = append(issues, Result{"path": name, "reason": "unreadable, nonregular, outside workspace or over hash budget"})
			continue
		}
		total += len(b)
		hashed++
		fmt.Fprintf(h, "%s\x00%s\x00", name, digest(b))
	}
	current := hex.EncodeToString(h.Sum(nil))
	state := "unbased"
	if len(missing)+len(unmapped)+len(issues) > 0 || len(files) == 0 {
		state = "incomplete"
	} else if r.ObservedHash != "" {
		state = "changed"
		if r.ObservedHash == current {
			state = "unchanged"
		}
	}
	return map[string]any{"state": state, "fingerprint": current, "hashed_files": hashed, "hashed_bytes": total, "missing": first(missing, 12), "missing_total": len(missing), "unmapped": first(unmapped, 12), "unmapped_total": len(unmapped), "issues": first(issues, 12), "issues_total": len(issues), "note": "Current graph bytes and selected nodes' source bytes only. Unchanged means equal to a supplied observation, not architecture correctness. Changes or incomplete coverage require source inspection; reads are not an atomic repository snapshot."}
}

func first(a []any, n int) []any {
	if a == nil {
		return []any{}
	}
	return a[:min(len(a), n)]
}
func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

type capBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.limit - b.Len()
	if n > left {
		b.overflow = true
		p = p[:max(0, left)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func (c *Client) engine(ctx context.Context, input Result) (Result, error) {
	if !filepath.IsAbs(c.Core) {
		return nil, errors.New("engine_unavailable: original DGV core path must be absolute")
	}
	if info, err := os.Stat(c.Core); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("engine_unavailable: install the original DGV core next to rfx-dgv or set RFX_DGV_CORE")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "node", "--input-type=module", "-e", bridge, c.Core)
	cmd.Stdin = bytes.NewReader(encoded)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8"}
	var stdout = capBuffer{limit: MaxGraphBytes + MaxInputBytes}
	var stderr = capBuffer{limit: 12000}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("engine_error: %s (%w)", stderr.String(), err)
	}
	if stdout.overflow {
		return nil, errors.New("engine_output_limit: original engine response too large")
	}
	var result Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("engine_response: %w", err)
	}
	return result, nil
}

func (c *Client) mutate(ctx context.Context, root *os.Root, action string, r request) (Result, error) {
	if err := root.MkdirAll("dgv/.rfx-locks", 0700); err != nil {
		return nil, err
	}
	lock, err := root.OpenFile("dgv/.rfx-locks/"+r.Name+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	name := "dgv/" + r.Name + ".dgv.json"
	var original []byte
	doc := r.Document
	if action == "create" {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("already_exists: create never replaces a graph")
		}
	} else {
		doc, original, err = load(root, r.Name)
		if err != nil {
			return nil, err
		}
		if digest(original) != r.ExpectedHash {
			return nil, errors.New("stale_graph: read the latest graph before updating")
		}
	}
	result, err := c.engine(ctx, Result{"action": action, "document": doc, "patch": r.Patch})
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(result["document"], "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxGraphBytes {
		return nil, errors.New("file_limit: resulting graph exceeds 2 MiB")
	}
	backup := ""
	if action == "update" {
		if err := root.MkdirAll("dgv/.rfx-backups", 0700); err != nil {
			return nil, err
		}
		backup = "dgv/.rfx-backups/" + r.Name + "-" + r.ExpectedHash + ".json"
		if err := writeExclusive(root, backup, original); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		backupInfo, err := root.Lstat(backup)
		if err != nil || !backupInfo.Mode().IsRegular() {
			return nil, errors.New("backup_verification_failed: backup must be a regular file, never a symlink")
		}
		copy, err := readBounded(root, backup, MaxGraphBytes)
		if err != nil || !bytes.Equal(copy, original) {
			return nil, errors.New("backup_verification_failed: original graph left unchanged")
		}
		if err := syncDirectory(root, "dgv/.rfx-backups"); err != nil {
			return nil, err
		}
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	tmp := "dgv/.rfx-" + hex.EncodeToString(random[:]) + ".tmp"
	if err := writeExclusive(root, tmp, encoded); err != nil {
		return nil, err
	}
	// Recheck immediately before replacement; another RFX writer holds the same
	// lock. External editors do not share this advisory lock (documented limit).
	if action == "update" {
		current, err := readBounded(root, name, MaxGraphBytes)
		if err != nil || digest(current) != r.ExpectedHash {
			return nil, errors.New("stale_graph: external edit detected; prepared temp file retained")
		}
		if err := root.Rename(tmp, name); err != nil {
			return nil, err
		}
	} else {
		// Link is atomic create-if-absent, so a concurrent editor's new graph
		// is never overwritten. Remove only our temp after verifying the copy.
		if err := root.Link(tmp, name); err != nil {
			return nil, fmt.Errorf("create_conflict: %w", err)
		}
	}
	written, err := readBounded(root, name, MaxGraphBytes)
	if err != nil || !bytes.Equal(written, encoded) {
		return nil, errors.New("write_verification_failed: graph write outcome requires inspection")
	}
	if action == "create" {
		if err := root.Remove(tmp); err != nil {
			return nil, fmt.Errorf("temp_cleanup_failed: graph created and verified; %w", err)
		}
	}
	if err := syncDirectory(root, "dgv"); err != nil {
		return nil, fmt.Errorf("write_durability_unknown: graph bytes verified; %w", err)
	}
	delete(result, "document")
	result["graph_sha256"] = digest(encoded)
	result["name"] = r.Name
	result["backup"] = backup
	result["path"] = name
	result["concurrency"] = "serialized against other RFX writers; hash rechecked before replace; external editors do not share the advisory lock"
	return result, nil
}

func syncDirectory(root *os.Root, name string) error {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func writeExclusive(root *os.Root, name string, b []byte) error {
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(b)
	syncErr := f.Sync()
	closeErr := f.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}
