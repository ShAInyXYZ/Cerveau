package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupRejectsAbsoluteAndTraversalPaths(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.conf")
	if err := os.WriteFile(outside, []byte("outside original"), 0600); err != nil {
		t.Fatal(err)
	}
	g := New(workspace)
	for _, path := range []string{outside, "../outside.conf"} {
		args, _ := json.Marshal(map[string]string{"path": path, "content": "new"})
		if _, err := g.Remediate("write", args, time.Unix(0, 0)); err == nil {
			t.Fatalf("out-of-workspace backup path %q accepted", path)
		}
	}
	if err := backupFile("", outside, time.Unix(0, 0)); err == nil {
		t.Fatal("backup without an explicit workspace was accepted")
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 2 {
		t.Fatalf("backup created an external side effect: entries=%v err=%v", entries, err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "outside original" {
		t.Fatalf("external file changed: %q err=%v", data, err)
	}
}

func TestBackupRejectsEscapingSourceSymlinks(t *testing.T) {
	for _, directoryLink := range []bool{false, true} {
		name := "file symlink"
		if directoryLink {
			name = "directory symlink"
		}
		t.Run(name, func(t *testing.T) {
			workspace, outside := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "outside.conf"), []byte("outside original"), 0600); err != nil {
				t.Fatal(err)
			}
			path, target := "linked.conf", filepath.Join(outside, "outside.conf")
			if directoryLink {
				path, target = "link/outside.conf", outside
				if err := os.Symlink(target, filepath.Join(workspace, "link")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(target, filepath.Join(workspace, path)); err != nil {
				t.Fatal(err)
			}
			if err := backupFile(workspace, path, time.Unix(0, 0)); err == nil {
				t.Fatal("backup followed a source symlink outside the workspace")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 1 {
				t.Fatalf("backup wrote outside the workspace: entries=%v err=%v", entries, err)
			}
			data, err := os.ReadFile(filepath.Join(outside, "outside.conf"))
			if err != nil || string(data) != "outside original" {
				t.Fatalf("external file changed: %q err=%v", data, err)
			}
		})
	}
}

func TestBackupCannotOverwritePreexistingSidecarSymlink(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	const stamp = "19700101-000000"
	if err := os.WriteFile(filepath.Join(workspace, "app.conf"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(outside, "untouched.txt")
	if err := os.WriteFile(external, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(workspace, "app.conf.bak."+stamp)
	if err := os.Symlink(external, base); err != nil {
		t.Fatal(err)
	}
	if err := backupFile(workspace, "app.conf", time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(external)
	if err != nil || string(data) != "external" {
		t.Fatalf("pre-planted backup symlink changed its external target: %q err=%v", data, err)
	}
	data, err = os.ReadFile(base + ".1")
	if err != nil || string(data) != "original" {
		t.Fatalf("exclusive fallback backup missing: %q err=%v", data, err)
	}
}

func TestBackupPreservesEarlierCopyAtSameTimestamp(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "app.conf")
	stamp := time.Unix(0, 0)
	for _, body := range []string{"first version", "second version"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := backupFile(workspace, "app.conf", stamp); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]string{
		"app.conf.bak.19700101-000000":   "first version",
		"app.conf.bak.19700101-000000.1": "second version",
	} {
		data, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil || string(data) != want {
			t.Fatalf("backup %s lost earlier content: %q want=%q err=%v", name, data, want, err)
		}
	}
}

func TestBackupAcceptsContainedRelativeSymlinkAndWorkspaceAlias(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "actual")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "app.conf"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("app.conf", filepath.Join(workspace, "alias.conf")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "workspace-alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	if err := backupFile(alias, "alias.conf", time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "alias.conf.bak.19700101-000000"))
	if err != nil || string(data) != "original" {
		t.Fatalf("contained symlink backup failed: %q err=%v", data, err)
	}
}
