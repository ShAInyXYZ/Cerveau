package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupGlobWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	files := []string{
		"main.go", "go.mod",
		"internal/tools/glob.go", "internal/tools/grep.go",
		"internal/rfx/rfx.go", "internal/rfx/rfx_test.go",
		"panel/src/App.svelte", "panel/package.json",
		"rfx/git-status.rfx.yaml", "docs/notes.md",
		"node_modules/junk/index.js", ".git/HEAD",
	}
	for _, f := range files {
		p := filepath.Join(ws, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	return ws
}

func runGlob(t *testing.T, ws, pattern, path string) string {
	t.Helper()
	g := NewGlob(ws)
	args, _ := json.Marshal(map[string]string{"pattern": pattern, "path": path})
	out, err := g.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("glob %q: %v", pattern, err)
	}
	return out
}

func TestGlobStarStar(t *testing.T) {
	ws := setupGlobWorkspace(t)
	out := runGlob(t, ws, "**/*.go", "")
	for _, want := range []string{"main.go", "internal/tools/glob.go", "internal/rfx/rfx.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("**/*.go missing %s in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "node_modules") || strings.Contains(out, ".git") {
		t.Errorf("skip dirs leaked into results:\n%s", out)
	}
}

func TestGlobSingleSegmentAndSubdir(t *testing.T) {
	ws := setupGlobWorkspace(t)
	out := runGlob(t, ws, "*.yaml", "rfx")
	if !strings.Contains(out, "git-status.rfx.yaml") {
		t.Fatalf("subdir glob missed: %s", out)
	}
	if strings.Contains(out, "package.json") {
		t.Fatal("single-segment * crossed directories")
	}
	out = runGlob(t, ws, "internal/*/rfx.go", "")
	if !strings.Contains(out, "internal/rfx/rfx.go") {
		t.Fatalf("mid-path * segment failed: %s", out)
	}
}

func TestGlobNoMatchAndEscapes(t *testing.T) {
	ws := setupGlobWorkspace(t)
	if out := runGlob(t, ws, "**/*.rs", ""); out != "no matches" {
		t.Fatalf("want 'no matches', got %q", out)
	}
	g := NewGlob(ws)
	args, _ := json.Marshal(map[string]string{"pattern": "../**/*.go"})
	if _, err := g.Execute(context.Background(), args); err == nil {
		t.Fatal("'..' pattern not rejected")
	}
	// Subdir path escaping the workspace must hit the jail.
	args, _ = json.Marshal(map[string]string{"pattern": "*", "path": "../../etc"})
	if _, err := g.Execute(context.Background(), args); err == nil {
		t.Fatal("escaping path not jailed")
	}
}
