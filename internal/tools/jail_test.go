package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink inside the workspace pointing out of it must not let a write escape.
func TestResolveBlocksSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	j := newJail(root)

	// link -> /some/dir/outside/the/workspace
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Writing through the symlink targets outside the jail; must be rejected.
	if _, err := j.resolve("link/passwd"); err == nil {
		t.Fatal("expected symlink escape to be rejected, got nil error")
	}
}

// A symlink in the MIDDLE of the path (not the leaf) must also be caught.
// ws/a is a real dir, ws/a/b is a symlink out, and we write ws/a/b/c.
func TestResolveBlocksIntermediateSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	j := newJail(root)

	if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "a", "b")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := j.resolve("a/b/c"); err == nil {
		t.Fatal("expected intermediate symlink escape to be rejected, got nil error")
	}
}

// A normal write to a not-yet-existing file inside the workspace must succeed.
func TestResolveAllowsFreshFileInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	j := newJail(root)

	got, err := j.resolve("sub/dir/new.txt")
	if err != nil {
		t.Fatalf("expected fresh path to resolve, got %v", err)
	}
	want := filepath.Join(root, "sub", "dir", "new.txt")
	if got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

// Lexical traversal out of the workspace stays blocked.
func TestResolveBlocksParentTraversal(t *testing.T) {
	root := t.TempDir()
	j := newJail(root)
	if _, err := j.resolve("../escape"); err == nil {
		t.Fatal("expected ../ traversal to be rejected")
	}
}
