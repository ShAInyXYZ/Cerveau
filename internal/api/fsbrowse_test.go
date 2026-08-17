package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The browser lists directories so a phone can pick a workspace without the
// machine's native dialog. It must never become a way to read the filesystem:
// directories only, no file contents, and nothing above the user's home.
func TestListDirsOnlyReturnsDirectories(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "project"), 0o755)
	os.MkdirAll(filepath.Join(root, ".hidden"), 0o755)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644)

	entries, err := listDirs(root, root)
	if err != nil {
		t.Fatalf("listDirs: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "project") {
		t.Errorf("a real directory must be listed: %v", names)
	}
	if strings.Contains(joined, "notes.txt") {
		t.Errorf("files must never be listed: %v", names)
	}
	if strings.Contains(joined, ".hidden") {
		t.Errorf("dotfiles are noise for picking a workspace: %v", names)
	}
}

// Escaping the root is the whole risk of a remote browser.
func TestListDirsRefusesEscape(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "inside"), 0o755)

	for _, bad := range []string{"..", "../..", filepath.Join(root, "..", ".."), "/etc"} {
		if _, err := listDirs(root, bad); err == nil {
			t.Errorf("listing %q must be refused", bad)
		}
	}
	// the root itself and paths under it are fine
	if _, err := listDirs(root, filepath.Join(root, "inside")); err != nil {
		t.Errorf("a path inside the root must be allowed: %v", err)
	}
}

// A symlink pointing outside must not become a door either.
func TestListDirsRefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(outside, "secret"), 0o755)
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := listDirs(root, link); err == nil {
		t.Error("a symlink out of the root must be refused")
	}
}
