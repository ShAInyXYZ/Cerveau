package rfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPackYAML = `
rfx: 1
pack: github
version: 1.0.0
author: shiny
description: Git workflow talents
`

func TestPackLoading(t *testing.T) {
	dir := t.TempDir()
	// A valid pack with two reflexes and docs.
	packDir := filepath.Join(dir, "github")
	os.MkdirAll(filepath.Join(packDir, "docs"), 0o755)
	writeFile(t, packDir, "pack.yaml", testPackYAML)
	writeFile(t, packDir, "git-status.rfx.yaml", validAliasFor("git-status"))
	writeFile(t, packDir, "git-diff.rfx.yaml", validAliasFor("git-diff"))
	writeFile(t, filepath.Join(packDir, "docs"), "git-ops.md", "# operator card")
	// A folder WITHOUT pack.yaml → notice, contents ignored.
	bare := filepath.Join(dir, "random")
	os.MkdirAll(bare, 0o755)
	writeFile(t, bare, "ghost.rfx.yaml", validAliasFor("ghost"))
	// A flat standalone reflex still works.
	writeFile(t, dir, "panel-build.rfx.yaml", validAliasFor("panel-build"))

	l := NewLoader(dir, knownCore)
	all := l.All()
	if len(all) != 3 {
		t.Fatalf("All() = %d, want 3 (2 pack + 1 standalone): %v", len(all), all)
	}
	packs := l.Packs()
	if len(packs) != 1 || packs[0].Pack != "github" {
		t.Fatalf("Packs() = %v", packs)
	}
	if len(packs[0].Docs) != 1 || packs[0].Docs[0] != "git-ops.md" {
		t.Fatalf("pack docs not discovered: %v", packs[0].Docs)
	}
	for _, r := range all {
		wantPack := ""
		if r.Name == "git-status" || r.Name == "git-diff" {
			wantPack = "github"
		}
		if r.Pack != wantPack {
			t.Errorf("%s: Pack = %q, want %q", r.Name, r.Pack, wantPack)
		}
	}
	notices := l.Notices()
	if len(notices) != 1 || !strings.Contains(notices[0], "random") || !strings.Contains(notices[0], "pack.yaml") {
		t.Fatalf("missing folder notice: %v", notices)
	}
	if _, ok := l.Get("ghost"); ok {
		t.Fatal("ghost reflex from pack-less folder was loaded")
	}
}

func validAliasFor(name string) string {
	return "rfx: 1\nname: " + name + "\ndescription: d\nrisk: dangerous\nkind: pipeline\nsteps:\n  - bash: \"true\"\n"
}

func TestPackValidationRejections(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"version", strings.Replace(testPackYAML, "rfx: 1", "rfx: 9", 1), "must be 1"},
		{"semver", strings.Replace(testPackYAML, "1.0.0", "v1", 1), "semver"},
		{"name", strings.Replace(testPackYAML, "pack: github", "pack: Git Hub", 1), "must match"},
		{"description", strings.Replace(testPackYAML, "description: Git workflow talents\n", "", 1), "description: required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := ParsePack([]byte(c.doc), "pack.yaml")
			if err != nil {
				t.Fatalf("ParsePack: %v", err)
			}
			if err := ValidatePack(p); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want substring %q", err, c.want)
			}
		})
	}
}

func TestStateEnableDisable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "panel-build.rfx.yaml", validAliasFor("panel-build"))
	writeFile(t, dir, "test-race.rfx.yaml", validAliasFor("test-race"))

	l := NewLoader(dir, knownCore)
	if got := l.List(); len(got) != 2 {
		t.Fatalf("initial List = %d", len(got))
	}
	if err := l.SetEnabled("test-race", false); err != nil {
		t.Fatal(err)
	}
	if got := l.List(); len(got) != 1 || got[0].Name != "panel-build" {
		t.Fatalf("after disable, List = %v", got)
	}
	if !l.Disabled("test-race") || l.Disabled("panel-build") {
		t.Fatal("Disabled() state wrong")
	}
	// State persists across loader instances (file, not memory).
	l2 := NewLoader(dir, knownCore)
	if !l2.Disabled("test-race") {
		t.Fatal("state did not persist to .state.json")
	}
	// Re-enable.
	if err := l2.SetEnabled("test-race", true); err != nil {
		t.Fatal(err)
	}
	l3 := NewLoader(dir, knownCore)
	if got := l3.List(); len(got) != 2 {
		t.Fatalf("after re-enable, List = %d", len(got))
	}
	// Unknown name errors, and the manifest file is never touched.
	if err := l.SetEnabled("nope", false); err == nil {
		t.Fatal("SetEnabled accepted unknown reflex")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test-race.rfx.yaml"))
	if !strings.Contains(string(data), "test-race") {
		t.Fatal("manifest was modified by a toggle")
	}
}

func TestCrossPackNameCollision(t *testing.T) {
	dir := t.TempDir()
	for _, pack := range []string{"alpha", "beta"} {
		pd := filepath.Join(dir, pack)
		os.MkdirAll(pd, 0o755)
		writeFile(t, pd, "pack.yaml", strings.Replace(testPackYAML, "pack: github", "pack: "+pack, 1))
		writeFile(t, pd, "same-name.rfx.yaml", validAliasFor("same-name"))
	}
	l := NewLoader(dir, knownCore)
	if got := l.All(); len(got) != 1 {
		t.Fatalf("collision: All() = %d, want 1 survivor", len(got))
	}
	var sawDup bool
	for _, e := range l.Errors() {
		if _, ok := e.Err.(*DuplicateError); ok {
			sawDup = true
		}
	}
	if !sawDup {
		t.Fatal("cross-pack collision not reported as DuplicateError")
	}
}
