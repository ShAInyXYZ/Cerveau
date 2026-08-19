package loop

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The guard counts tool ERRORS, which makes calm, confident uselessness
// invisible to it. The two worst runs of the benchmark both had almost no
// errors:
//
//	v6  — 39 iterations, 17 edits, 1 page check. index.html moved ONE byte in
//	      four minutes while the model rewrote a Puppeteer script.
//	v10 — 46 iterations, 43 bash calls, 1 write, no fan produced.
//
// What separates those from real work is not error count, tool count, or
// duration. It is whether the ARTIFACT changed. A model that writes files is
// working however slowly; a model that has not touched the workspace in eight
// iterations is circling.

// progressStallLimit is how many consecutive iterations may leave the workspace
// byte-identical before the turn is stopped.
//
// 8 is deliberately loose. Reading, grepping and planning are legitimate
// non-writing work, and a build can reasonably spend several turns
// investigating before it edits. v6 and v10 both blew past 20 such iterations,
// so this catches them with a wide margin rather than clipping careful work.
const progressStallLimit = 8

// workTracker fingerprints the workspace so the loop can tell progress from
// churn. Names, sizes and mtimes only — hashing file CONTENT every iteration
// would cost more than it is worth on a large tree, and a build that rewrites a
// file with identical bytes is not making progress anyway.
type workTracker struct {
	root  string
	last  uint64
	stall int
}

func newWorkTracker(root string) *workTracker {
	return &workTracker{root: root}
}

// check fingerprints the workspace and reports whether it has been unchanged
// for too long. Returns a detail string when stuck.
func (w *workTracker) check() (string, bool) {
	if w.root == "" {
		return "", false // no workspace: nothing to measure, never block
	}
	fp, files := w.fingerprint()
	if fp != w.last {
		w.last = fp
		w.stall = 0
		return "", false
	}
	w.stall++
	if w.stall < progressStallLimit {
		return "", false
	}
	w.stall = 0 // one stop per stall, not one per iteration after
	return fmt.Sprintf("%d iterations with no change to any file in the workspace (%s) — "+
		"tool calls are being made but nothing is being built. Either the current approach "+
		"is not working, or the task is already done and needs to be reported.",
		progressStallLimit, strings.Join(files, ", ")), true
}

// fingerprint hashes the visible file tree: name, size, mtime.
func (w *workTracker) fingerprint() (uint64, []string) {
	var names []string
	h := sha256.New()
	_ = filepath.WalkDir(w.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			// skip the noise a build drops: none of it is the artifact
			if name == "node_modules" || name == ".git" || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		rel, _ := filepath.Rel(w.root, path)
		fmt.Fprintf(h, "%s:%d:%d\n", rel, info.Size(), info.ModTime().UnixNano())
		names = append(names, rel)
		return nil
	})
	sort.Strings(names)
	if len(names) > 8 {
		names = append(names[:8], "...")
	}
	if len(names) == 0 {
		names = []string{"empty"}
	}
	return binary.BigEndian.Uint64(h.Sum(nil)[:8]), names
}
