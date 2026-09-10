package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRecoveryShellCannotTruncateSource(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap unavailable; production fails closed")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "world.js")
	if err := os.WriteFile(file, []byte("original\ncomplete\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"command": "head -n 1 world.js > world.tmp && mv world.tmp world.js"})
	if _, err := NewBash(dir).Execute(WithRecoveryShell(context.Background()), args); err == nil {
		t.Fatal("source write succeeded")
	}
	got, _ := os.ReadFile(file)
	if string(got) != "original\ncomplete\n" {
		t.Fatal("source was changed")
	}
	args, _ = json.Marshal(map[string]string{"command": "test -s world.js"})
	if out, err := NewBash(dir).Execute(WithRecoveryShell(context.Background()), args); err != nil {
		t.Fatalf("read-only check: %s %v", out, err)
	}
}

func TestRecoveryShellUnavailableFailsClosed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if _, err := NewBash(dir).Execute(WithRecoveryShell(context.Background()), json.RawMessage(`{"command":"touch should-not-exist"}`)); err == nil {
		t.Fatal("unprotected fallback")
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("command executed")
	}
}
