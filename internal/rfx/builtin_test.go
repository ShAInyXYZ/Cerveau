package rfx

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinPlannerUsesCanonicalSource(t *testing.T) {
	p, err := BuiltinPlanner()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("../../rfx/planner/pack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	panel, err := os.ReadFile("../../rfx/planner/ui/panel.html")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePack(manifest, "pack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.New()
	for _, f := range []struct {
		name string
		data []byte
	}{{"pack.yaml", manifest}, {"ui/panel.html", panel}} {
		want.Write([]byte(f.name + "\x00"))
		want.Write(f.data)
		want.Write([]byte{0})
	}
	got, err := p.ReadPanel()
	if err != nil {
		t.Fatal(err)
	}
	if p.Pack != "planner" || p.Origin != "builtin" || p.Version != parsed.Version || p.ContentSHA256 != fmt.Sprintf("%x", want.Sum(nil)) || !bytes.Equal(got, panel) {
		t.Fatalf("canonical bundle mismatch: %+v", p)
	}
}

func TestBuiltinPlannerLoadsWithoutInstalledDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	l := NewLoader(dir, knownCore, WithBuiltinPlanner())
	for i := 0; i < 2; i++ {
		l.Scan()
		packs := l.Packs()
		if len(packs) != 1 || packs[0].Pack != "planner" || packs[0].Origin != "builtin" || len(l.Errors()) != 0 {
			t.Fatalf("fresh install: packs=%+v errors=%v", packs, l.Errors())
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read-only loader created directory: %v", err)
	}
}

func TestBuiltinPlannerIgnoresInstalledCopiesWithoutMutation(t *testing.T) {
	builtin, err := BuiltinPlanner()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, folder, manifest, version string }{
		{"old", "planner", "rfx: 1\npack: planner\nversion: 0.1.0\ndescription: stale\n", "0.1.0"},
		{"equal", "planner", "rfx: 1\npack: planner\nversion: " + builtin.Version + "\ndescription: modified\n", builtin.Version},
		{"newer", "planner", "rfx: 1\npack: planner\nversion: 999.0.0\ndescription: incompatible\n", "999.0.0"},
		{"malformed", "planner", "not: [valid\n", ""},
		{"renamed", "other-name", "rfx: 1\npack: planner\nversion: 0.2.0\ndescription: renamed\n", "0.2.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			copyDir := filepath.Join(dir, tc.folder)
			if err := os.MkdirAll(filepath.Join(copyDir, "ui"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, copyDir, "pack.yaml", tc.manifest)
			writeFile(t, filepath.Join(copyDir, "ui"), "panel.html", "<p>DO NOT SERVE INSTALLED PLANNER</p>")
			writeFile(t, copyDir, "injected.rfx.yaml", validAliasFor("injected"))
			before := map[string][]byte{}
			for _, file := range []string{"pack.yaml", "ui/panel.html", "injected.rfx.yaml"} {
				data, err := os.ReadFile(filepath.Join(copyDir, file))
				if err != nil {
					t.Fatal(err)
				}
				before[file] = data
			}
			l := NewLoader(dir, knownCore, WithBuiltinPlanner())
			for i := 0; i < 2; i++ {
				l.Scan()
				packs := l.Packs()
				if len(packs) != 1 || packs[0].ContentSHA256 != builtin.ContentSHA256 || packs[0].Origin != "builtin" {
					t.Fatalf("shadowed: %+v", packs)
				}
				ignored := packs[0].IgnoredInstalled
				if len(ignored) != 1 || ignored[0].Path != copyDir || ignored[0].Version != tc.version {
					t.Fatalf("ignored identity: %+v", ignored)
				}
				if len(l.All()) != 0 || len(l.Errors()) != 0 || !strings.Contains(strings.Join(l.Notices(), "\n"), "ignored") {
					t.Fatalf("copy injected or silently hidden: %+v / %v / %v", l.All(), l.Errors(), l.Notices())
				}
				panel, err := packs[0].ReadPanel()
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(panel, []byte("DO NOT SERVE")) {
					t.Fatal("served installed copy")
				}
			}
			for file, want := range before {
				got, err := os.ReadFile(filepath.Join(copyDir, file))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("installed file changed: %s: %v", file, err)
				}
			}
		})
	}
}

func TestBuiltinPlannerPreservesExternalPacks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "github", "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "github"), "pack.yaml", testPackYAML)
	writeFile(t, filepath.Join(dir, "github", "ui"), "panel.html", "<p>external UI</p>")
	writeFile(t, filepath.Join(dir, "github"), "git-status.rfx.yaml", validAliasFor("git-status"))
	l := NewLoader(dir, knownCore, WithBuiltinPlanner())
	if len(l.Packs()) != 2 || len(l.All()) != 1 || len(l.Errors()) != 0 {
		t.Fatalf("external pack lost: %+v %v", l.Packs(), l.Errors())
	}
	for _, p := range l.Packs() {
		if p.Pack == "github" {
			data, err := p.ReadPanel()
			if err != nil || p.Origin != "installed" || string(data) != "<p>external UI</p>" {
				t.Fatalf("external panel: %+v %v", p, err)
			}
		}
	}
}

func TestBuiltinPlannerReportsAllShadowedIdentitiesWithDetachedSnapshots(t *testing.T) {
	dir := t.TempDir()
	for _, folder := range []string{"planner", "renamed-planner"} {
		if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, folder), "pack.yaml", "rfx: 1\npack: planner\nversion: 0.1.0\ndescription: stale\n")
	}
	l := NewLoader(dir, knownCore, WithBuiltinPlanner())
	first := l.Packs()
	if len(first) != 1 || len(first[0].IgnoredInstalled) != 2 {
		t.Fatalf("missing copies: %+v", first)
	}
	first[0].IgnoredInstalled[0].Version = "mutated-by-observer"
	if got := l.Packs()[0].IgnoredInstalled[0].Version; got != "0.1.0" {
		t.Fatalf("observer mutated loader snapshot: %s", got)
	}
	l.Scan()
	if got := len(l.Packs()[0].IgnoredInstalled); got != 2 {
		t.Fatalf("rescan accumulated duplicates: %d", got)
	}
}
