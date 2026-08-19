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
