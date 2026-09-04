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
// 4. It was 8, and 8 iterations of node probes that all fail on the model's
// own test lines is two minutes of watching nothing get built (2026-09-04).
// Reading and grepping before an edit rarely needs more than three calls;
// a fourth without touching a file is when the model should hear about it.
const progressStallLimit = 4

// progressStallStop is where a nudge becomes a stop. A nudge that resets the
// counter can be repeated forever — 8, 16, 24 iterations of nothing, each one
// "the first". Four more iterations after the nudge is enough to change
// approach or to say the work is done; after that the user decides.
const progressStallStop = progressStallLimit + 4

// workTracker fingerprints the workspace so the loop can tell progress from
// churn. Names, sizes and mtimes only — hashing file CONTENT every iteration
// would cost more than it is worth on a large tree, and a build that rewrites a
// file with identical bytes is not making progress anyway.
type workTracker struct {
	root  string
	last  uint64
	stall int
	// changed is set by check() when the fingerprint moved: the artifact is
	// different, which is the one kind of progress that cannot be faked.
	changed bool
	// stopping is set when the stall has outlived its nudge.
	stopping bool
}

func newWorkTracker(root string) *workTracker {
	return &workTracker{root: root}
}

// credit forgives the stall for work that is real but leaves no trace on disk.
//
// The fingerprint answers "did the artifact change", which is the right
// question for a model that is churning. It is the WRONG question for the
// last phase of a build: verifying a page, reading back a result, proving a
// control works. That is what the prompt asks for, it produces no file, and
// it used to be indistinguishable from circling.
//
// The caller passes only a SUCCESSFUL tool result the turn has not seen
// before, so a probe that fails the same way eight times still stalls out and
// a repeated identical call earns nothing. One credit clears the counter, and
// the nudge is allowed to fire again from zero.
func (w *workTracker) credit() {
	w.stall = 0
	w.stopping = false
}

// check fingerprints the workspace and reports whether it has been unchanged
// for too long. Returns a detail string when stuck.
func (w *workTracker) check() (string, bool) {
	if w.root == "" {
		return "", false // no workspace: nothing to measure, never block
	}
	fp, files := w.fingerprint()
	w.stopping = false
	if fp != w.last {
		w.last = fp
		w.stall = 0
		w.changed = true
		return "", false
	}
	w.changed = false
	w.stall++
	switch {
	case w.stall == progressStallLimit:
		return fmt.Sprintf("%d iterations with no change to any file in the workspace (%s) — "+
			"tool calls are being made but nothing is being built. Either the current approach "+
			"is not working, or the task is already done and needs to be reported.",
			progressStallLimit, strings.Join(files, ", ")), true
	case w.stall >= progressStallStop:
		w.stall = 0
		w.stopping = true
		return fmt.Sprintf("%d iterations with no change to any file in the workspace, "+
			"including %d after being told so — stopping so the user can redirect",
			progressStallStop, progressStallStop-progressStallLimit), true
	}
	return "", false
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
