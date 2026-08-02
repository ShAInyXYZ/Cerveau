package api

import (
	"os"
	"path/filepath"
	"testing"
)

// The panel needs ground truth: which of a plan step's declared files
// actually exist, and how big they are. Paths are workspace-relative and
// must not escape it.
func TestProbeFiles(t *testing.T) {
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, "js"), 0o755)
	os.WriteFile(filepath.Join(ws, "js", "ball.js"), []byte("012345678"), 0o644)

	got := probeFiles(ws, []string{"js/ball.js", "css/style.css", "../escape.txt"})
	if len(got) != 3 {
		t.Fatalf("want 3 results, got %d: %+v", len(got), got)
	}
	if !got[0].Exists || got[0].Bytes != 9 {
		t.Fatalf("existing file wrong: %+v", got[0])
	}
	if got[1].Exists {
		t.Fatalf("missing file reported as existing: %+v", got[1])
	}
	// an escaping path is never probed outside the workspace
	if got[2].Exists {
		t.Fatalf("path escaped the workspace: %+v", got[2])
	}
}
