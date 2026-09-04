package loop

import (
	"os"
	"path/filepath"
	"testing"
)

// The guard counts ERRORS. Both runs that wasted the most time today had almost
// none:
//
//	v6  — 39 iterations, 17 edits, 1 page check; index.html moved ONE byte in
//	      four minutes while it rewrote a Puppeteer script
//	v10 — 46 iterations, 43 bash calls, 1 write, no fan at all
//
// Neither tripped anything, because calmly doing nothing is invisible to an
// error counter. The signal that separates work from churn is whether the
// ARTIFACT changed.
func TestStuckWhenArtifactStopsChanging(t *testing.T) {
	dir := t.TempDir()
	w := newWorkTracker(dir)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>start</html>"), 0o644)

	if _, stuck := w.check(); stuck {
		t.Fatal("stuck on the very first check")
	}
	// the model now spins: same file, same bytes, many iterations
	for i := 0; i < progressStallLimit-1; i++ {
		if _, stuck := w.check(); stuck {
			t.Fatalf("tripped early at iteration %d", i+1)
		}
	}
	detail, stuck := w.check()
	if !stuck {
		t.Fatalf("workspace unchanged for %d checks and nothing fired", progressStallLimit)
	}
	if detail == "" {
		t.Error("no detail to tell the model WHY it was stopped")
	}
}

// Real work must never trip it, however slow. A model writing files is
// progressing even if it takes many iterations.
func TestWritingFilesIsNeverStuck(t *testing.T) {
	dir := t.TempDir()
	w := newWorkTracker(dir)
	for i := 0; i < progressStallLimit*3; i++ {
		os.WriteFile(filepath.Join(dir, "index.html"), []byte(string(rune('a'+i%26))+"content"), 0o644)
		if _, stuck := w.check(); stuck {
			t.Fatalf("tripped while files were actively changing, iteration %d", i+1)
		}
	}
}

// A new file counts as progress even if existing ones are untouched.
func TestNewFileCountsAsProgress(t *testing.T) {
	dir := t.TempDir()
	w := newWorkTracker(dir)
	os.WriteFile(filepath.Join(dir, "a.js"), []byte("x"), 0o644)
	for i := 0; i < progressStallLimit-1; i++ {
		w.check()
	}
	os.WriteFile(filepath.Join(dir, "b.js"), []byte("y"), 0o644)
	if _, stuck := w.check(); stuck {
		t.Error("adding a new file should reset the stall counter")
	}
}

// A nudge that resets the counter can be repeated forever. After the nudge
// the model gets a few more iterations, then the tracker says stop.
func TestStallEscalatesFromNudgeToStop(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644)
	w := newWorkTracker(dir)
	w.check() // baseline
	nudged, stopped := 0, 0
	for i := 0; i < progressStallStop+2; i++ {
		if _, stuck := w.check(); stuck {
			if w.stopping {
				stopped++
			} else {
				nudged++
			}
		}
	}
	if nudged != 1 || stopped != 1 {
		t.Fatalf("expected one nudge then one stop, got nudged=%d stopped=%d", nudged, stopped)
	}
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("xy"), 0o644)
	w.check()
	if !w.changed {
		t.Fatal("a changed file must register as change")
	}
}

// Verification is work, and it leaves no trace on disk.
//
// The stall counter measures file changes, so a build that finished writing
// and moved on to checking its own output looked identical to a model
// circling. The fan run wrote three files, then was killed 45 seconds later
// for "8 iterations with no change" while running the checks the prompt had
// explicitly asked for (2026-09-04).
func TestCreditForgivesVerificationThatWritesNothing(t *testing.T) {
	dir := t.TempDir()
	w := newWorkTracker(dir)
	w.check() // baseline

	// three verification iterations, no file touched
	for i := 0; i < 3; i++ {
		w.check()
	}
	if w.stall == 0 {
		t.Fatal("setup: the tracker should be counting a stall")
	}

	// a new, successful tool result — the caller only credits those
	w.credit()
	if w.stall != 0 {
		t.Fatalf("credit must clear the stall, got %d", w.stall)
	}
	if w.stopping {
		t.Fatal("credit must clear the stop flag too")
	}

	// and the nudge is allowed to fire again from zero, so a model that
	// verifies once then genuinely circles is still caught
	var nudged bool
	for i := 0; i < progressStallLimit; i++ {
		if _, stuck := w.check(); stuck {
			nudged = true
		}
	}
	if !nudged {
		t.Fatal("after a credit the counter must still be able to reach the nudge")
	}
}
