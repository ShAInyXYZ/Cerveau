package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func remediateBash(t *testing.T, g *Guard, cmd string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"command": cmd})
	out, err := g.Remediate("bash", args, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("remediate error: %v", err)
	}
	var a struct {
		Command string `json:"command"`
	}
	json.Unmarshal(out, &a)
	return a.Command
}

func TestMvRewrittenToCopyVerifyDelete(t *testing.T) {
	g := New(t.TempDir())
	got := remediateBash(t, g, "mv a.txt b.txt")
	if !strings.Contains(got, "cp -a a.txt b.txt") {
		t.Fatalf("no copy step: %q", got)
	}
	if !strings.Contains(got, "rm -rf a.txt") {
		t.Fatalf("no delete step: %q", got)
	}
	if !strings.Contains(got, "[ -e b.txt ]") {
		t.Fatalf("no verify guard before delete: %q", got)
	}
	// copy must come before delete
	if strings.Index(got, "cp -a") > strings.Index(got, "rm -rf") {
		t.Fatalf("delete precedes copy: %q", got)
	}
}

func TestMvWithFlagsOrGlobsNotRewritten(t *testing.T) {
	g := New(t.TempDir())
	for _, cmd := range []string{
		"mv -f a b",          // has a flag — leave it, don't guess
		"mv a b c",           // multiple sources
		"mv *.go dir/",       // glob
		"mv a b && rm -rf /", // compound — must not be treated as a bare mv
	} {
		got := remediateBash(t, g, cmd)
		if got != cmd {
			t.Fatalf("should not rewrite %q -> %q", cmd, got)
		}
	}
}

func TestNonMvCommandUntouched(t *testing.T) {
	g := New(t.TempDir())
	if got := remediateBash(t, g, "ls -la"); got != "ls -la" {
		t.Fatalf("touched non-mv: %q", got)
	}
}

func TestSensitiveFileBackedUpBeforeWrite(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "app.conf"), []byte("original"), 0o644)
	g := New(ws)

	args, _ := json.Marshal(map[string]any{"path": "app.conf", "content": "new"})
	_, err := g.Remediate("write", args, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	// a .bak.<ts> sidecar must now exist with the ORIGINAL content
	entries, _ := os.ReadDir(ws)
	var bak string
	for _, e := range entries {
		if strings.Contains(e.Name(), "app.conf.bak.") {
			bak = filepath.Join(ws, e.Name())
		}
	}
	if bak == "" {
		t.Fatal("no backup created for sensitive file")
	}
	data, _ := os.ReadFile(bak)
	if string(data) != "original" {
		t.Fatalf("backup has wrong content: %q", data)
	}
}

func TestNewFileNeedsNoBackup(t *testing.T) {
	ws := t.TempDir()
	g := New(ws)
	args, _ := json.Marshal(map[string]any{"path": "brand-new.conf", "content": "x"})
	if _, err := g.Remediate("write", args, time.Unix(0, 0)); err != nil {
		t.Fatalf("new file should not error: %v", err)
	}
	// nothing to back up -> no .bak files
	entries, _ := os.ReadDir(ws)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			t.Fatalf("unexpected backup for new file: %s", e.Name())
		}
	}
}

func TestOrdinaryCodeFileNotBackedUp(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "main.go_src"), []byte("x"), 0o644)
	g := New(ws)
	// a normal source file (not important-path) — no backup churn
	args, _ := json.Marshal(map[string]any{"path": "hello.txt", "content": "y"})
	g.Remediate("edit", args, time.Unix(0, 0))
	entries, _ := os.ReadDir(ws)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			t.Fatalf("backed up an ordinary file: %s", e.Name())
		}
	}
}

// The rm rule must judge against the ACTUAL workspace boundary, not "starts
// with a slash": an absolute path inside the workspace is a normal delete;
// outside it (or root, home, bare globs, traversal) stays catastrophic.
func TestRmWorkspaceBoundary(t *testing.T) {
	g := New("/ws/project")

	allowed := []string{
		`rm -rf /ws/project/dist`,
		`rm -rf /ws/project/a.html /ws/project/b.html`,
		`rm -rf build/`,
		`rm -f old.txt`,
		`rm -rf dist/*`,
	}
	for _, cmd := range allowed {
		if err := g.Check("bash", bashArgs(cmd)); err != nil {
			t.Errorf("should be allowed (inside workspace): %q → %v", cmd, err)
		}
	}

	blocked := []string{
		`rm -rf /`,
		`rm -rf /etc`,
		`rm -rf /ws/other`,
		`rm -rf ~`,
		`rm -rf $HOME`,
		`rm -rf *`,
		`rm -rf ../sibling`,
		`rm -rf /ws/project/../other`,
	}
	for _, cmd := range blocked {
		if err := g.Check("bash", bashArgs(cmd)); err == nil {
			t.Errorf("should be blocked: %q", cmd)
		}
	}
}

// The guard must follow runtime workspace changes — it was frozen at startup,
// so after a workspace switch it judged against a stale root.
func TestGuardSetWorkspace(t *testing.T) {
	g := New("/old/root")
	if err := g.Check("bash", bashArgs(`rm -rf /new/root/file.txt`)); err == nil {
		t.Fatal("path outside current workspace should be blocked")
	}
	g.SetWorkspace("/new/root")
	if err := g.Check("bash", bashArgs(`rm -rf /new/root/file.txt`)); err != nil {
		t.Fatalf("after SetWorkspace the path is inside: %v", err)
	}
}

func bashArgs(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}
