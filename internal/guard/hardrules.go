package guard

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Hard rules are STRUCTURAL safety enforced in Go at dispatch — not prompt text
// the model can ignore. They apply in every mode. Two behaviours:
//
//   BLOCK      — no safe form exists (rm -rf /, force-push, DROP, ...). Handled by
//                the existing catastrophic cmdRules in Check().
//   REMEDIATE  — a safe form exists, so the harness rewrites the action to it:
//                  * bash `mv A B`         -> `cp -a A B && <verify> && rm -rf A`
//                  * edit/write <file>     -> back up <file> to <file>.bak.<ts> first
//
// Remediate transforms the tool ARGS (and may perform a side effect like a backup)
// and returns the new args. If it returns an error, the action is blocked.

// bareMv matches a plain `mv SRC DST` we can safely rewrite. Deliberately
// conservative: single source, no flags, no globs/pipes/redirects/multiple args.
// Anything fancier falls through untouched (the model can do copy-verify itself).
var bareMv = regexp.MustCompile(`^\s*mv\s+("[^"]+"|'[^']+'|[^\s'"]+)\s+("[^"]+"|'[^']+'|[^\s'"]+)\s*$`)

// Remediate inspects a tool call and returns rewritten args. workspace is the
// jail root; ts is the timestamp source (injected for testability).
func (g *Guard) Remediate(tool string, args json.RawMessage, now time.Time) (json.RawMessage, error) {
	switch tool {
	case "bash":
		var a struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(args, &a) != nil {
			return args, nil
		}
		if rewritten, ok := remediateMv(a.Command); ok {
			a.Command = rewritten
			return json.Marshal(a)
		}
		return args, nil

	case "edit", "write":
		var a struct {
			Path    string          `json:"path"`
			Content json.RawMessage `json:"content,omitempty"`
		}
		if json.Unmarshal(args, &a) != nil || a.Path == "" {
			return args, nil
		}
		// Only back up files the guard considers important AND that already exist.
		if importantPath(a.Path) {
			if err := backupFile(g.ws(), a.Path, now); err != nil {
				return nil, fmt.Errorf("refusing to modify %q: backup failed: %w", a.Path, err)
			}
		}
		return args, nil
	}
	return args, nil
}

// remediateMv turns a bare `mv A B` into a copy-verify-delete chain.
// Returns (newCommand, true) when it rewrote, (original, false) otherwise.
func remediateMv(cmd string) (string, bool) {
	m := bareMv.FindStringSubmatch(cmd)
	if m == nil {
		return cmd, false
	}
	src, dst := m[1], m[2]
	// Refuse to rewrite if either operand contains shell metacharacters we can't
	// reason about safely (globs, expansions, subshells). Rewriting `mv *.go dir/`
	// into `rm -rf *.go` would be catastrophic — leave those for the model.
	if hasGlobOrExpansion(src) || hasGlobOrExpansion(dst) {
		return cmd, false
	}
	// cp -a preserves attributes; `[ -e dst ]` verifies the copy landed; only then
	// rm the original — so a failed copy never loses data.
	safe := fmt.Sprintf(`cp -a %s %s && [ -e %s ] && rm -rf %s`, src, dst, dst, src)
	return safe, true
}

func hasGlobOrExpansion(s string) bool {
	return strings.ContainsAny(s, "*?[]{}$`~")
}

// importantPath reports whether a path deserves a pre-edit backup. Kept broad but
// cheap: config/env-ish files and lockfiles. (Secrets are blocked outright by
// pathRules, so they never reach here.)
func importantPath(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	switch {
	case strings.HasSuffix(base, ".env"), strings.Contains(base, ".env."):
		return true
	case base == "dockerfile", strings.HasSuffix(base, ".conf"), strings.HasSuffix(base, ".cfg"),
		strings.HasSuffix(base, ".ini"), strings.HasSuffix(base, ".toml"), strings.HasSuffix(base, ".yaml"),
		strings.HasSuffix(base, ".yml"):
		return true
	case base == "go.mod", base == "package.json", base == "cargo.toml", base == "config.json":
		return true
	}
	return false
}

// backupFile copies workspace/path to path.bak.<ts> if the target exists.
// A missing target is fine (new file — nothing to back up).
func backupFile(workspace, path string, now time.Time) error {
	// Tool paths are workspace-relative. This side effect happens before the
	// tool's own jail validation, so it needs an independent traversal-safe
	// boundary. Root protects both reads and backup creation from symlink swaps.
	if workspace == "" || !filepath.IsLocal(path) {
		return fmt.Errorf("backup path %q must be relative to the workspace", path)
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // creating a new file — nothing to back up
		}
		return err
	}
	if info.IsDir() {
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup source %q is not a regular file", path)
	}
	src, err := root.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err = src.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup source %q is not a regular file", path)
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	base := fmt.Sprintf("%s.bak.%s", path, now.UTC().Format("20060102-150405"))
	for attempt := 0; attempt < 100; attempt++ {
		bak := base
		if attempt > 0 {
			bak = fmt.Sprintf("%s.%d", base, attempt)
		}
		// O_EXCL protects previous backups and refuses pre-planted symlinks.
		dst, err := root.OpenFile(bak, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		_, writeErr := dst.Write(data)
		if writeErr == nil {
			writeErr = dst.Sync()
		}
		closeErr := dst.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	return fmt.Errorf("cannot create a distinct backup for %q", path)
}
