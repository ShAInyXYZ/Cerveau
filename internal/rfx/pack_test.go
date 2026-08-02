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

// Widget semantics are load-time errors, not runtime surprises in the panel:
// rows regexes must compile, every must parse, pack-level buttons need run.
func TestPackWidgetSemanticRejections(t *testing.T) {
	base := testPackYAML + "ui:\n  widgets:\n"
	cases := []struct{ name, widgets, want string }{
		{"bad rows regex", "    - {type: status, run: git-status, rows: {branch: '(['}}\n", "does not compile"},
		{"bad every", "    - {type: status, run: git-status, every: soonish, rows: {branch: 'x'}}\n", "every"},
		{"pack button needs run", "    - {type: button, label: Go}\n", "run: required"},
		{"bad row tone", "    - {type: status, run: git-status, rows: {added: {re: 'x', tone: rainbow}}}\n", "tone"},
		{"bad row map regex", "    - {type: status, run: git-status, rows: {added: {re: '(', tone: ok}}}\n", "does not compile"},
		{"list needs match", "    - {type: list}\n", "match: required"},
		{"list bad match", "    - {type: list, match: '(['}\n", "does not compile"},
		{"unknown icon", "    - {type: button, label: Go, run: git-status, icon: dragon}\n", "icon"},
		{"on_fail needs label+run", "    - {type: status, run: git-status, rows: {b: 'x'}, on_fail: {label: Fix}}\n", "on_fail"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := ParsePack([]byte(base+c.widgets), "pack.yaml")
			if err != nil {
				t.Fatalf("ParsePack: %v", err)
			}
			if err := ValidatePack(p); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want substring %q", err, c.want)
			}
		})
	}
}

// Rows accept both forms: scalar (label: regex) and map (label: {re, tone}).
func TestRowSpecBothForms(t *testing.T) {
	doc := testPackYAML + `ui:
  widgets:
    - type: status
      run: git-status
      rows:
        branch: '## (\S+)'
        added: {re: '(\d+) insertion', tone: ok}
        removed: {re: '(\d+) deletion', tone: err}
`
	p, err := ParsePack([]byte(doc), "pack.yaml")
	if err != nil {
		t.Fatalf("ParsePack: %v", err)
	}
	if err := ValidatePack(p); err != nil {
		t.Fatalf("ValidatePack: %v", err)
	}
	rows := p.UI.Widgets[0].Rows
	if rows["branch"].Re != `## (\S+)` || rows["branch"].Tone != "" {
		t.Fatalf("scalar row: %+v", rows["branch"])
	}
	if rows["added"].Tone != "ok" || rows["removed"].Tone != "err" {
		t.Fatalf("map rows: %+v %+v", rows["added"], rows["removed"])
	}
}

// on_fail.run must resolve like any widget run: — a dangling remedy is
// rejected with the pack at load.
func TestOnFailRunMustResolve(t *testing.T) {
	dir := t.TempDir()
	pd := filepath.Join(dir, "github")
	os.MkdirAll(pd, 0o755)
	writeFile(t, pd, "pack.yaml", testPackYAML+
		"ui:\n  widgets:\n    - {type: status, run: git-status, rows: {b: 'x'}, on_fail: {label: Init, run: no-such}}\n")
	writeFile(t, pd, "git-status.rfx.yaml", validAliasFor("git-status"))
	l := NewLoader(dir, knownCore)
	if packs := l.Packs(); len(packs) != 0 {
		t.Fatalf("pack with dangling on_fail.run was loaded: %v", packs)
	}
}

// A widget run: naming a reflex that doesn't exist rejects the PACK at load
// (loud), instead of failing later inside the panel.
func TestPackWidgetRunMustResolve(t *testing.T) {
	dir := t.TempDir()
	pd := filepath.Join(dir, "github")
	os.MkdirAll(pd, 0o755)
	writeFile(t, pd, "pack.yaml", testPackYAML+
		"ui:\n  widgets:\n    - {type: button, label: Go, run: no-such-reflex}\n")
	writeFile(t, pd, "git-status.rfx.yaml", validAliasFor("git-status"))

	l := NewLoader(dir, knownCore)
	if packs := l.Packs(); len(packs) != 0 {
		t.Fatalf("pack with dangling run: was loaded: %v", packs)
	}
	found := false
	for _, e := range l.Errors() {
		if strings.Contains(e.Err.Error(), "no-such-reflex") {
			found = true
		}
	}
	if !found {
		t.Fatalf("dangling run: not reported: %v", l.Errors())
	}
}

// An exec reflex declaring a network allowlist gets a loader NOTICE: the
// card cannot enforce it pre-Landlock (honest scoping, never silent).
func TestExecNetworkAllowlistNotice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "homelab-temps.rfx.yaml", `
rfx: 1
name: homelab-temps
description: d
risk: safe
kind: exec
argv: [/opt/homelab/temps]
card: {network: [homelab.local], subprocess: true}
`)
	l := NewLoader(dir, knownCore)
	if got := l.All(); len(got) != 1 {
		t.Fatalf("exec reflex rejected: %v", l.Errors())
	}
	found := false
	for _, n := range l.Notices() {
		if strings.Contains(n, "homelab-temps") && strings.Contains(n, "network") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unenforceable-network notice: %v", l.Notices())
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

// A pack may ship ui/panel.html — full custom UI, discovered at load.
// Oversized panels are rejected loudly (the file is served to the iframe).
func TestPanelDiscovery(t *testing.T) {
	dir := t.TempDir()
	pd := filepath.Join(dir, "github")
	os.MkdirAll(filepath.Join(pd, "ui"), 0o755)
	writeFile(t, pd, "pack.yaml", testPackYAML)
	writeFile(t, pd, "git-status.rfx.yaml", validAliasFor("git-status"))
	writeFile(t, filepath.Join(pd, "ui"), "panel.html", "<h1>custom</h1>")

	l := NewLoader(dir, knownCore)
	packs := l.Packs()
	if len(packs) != 1 || packs[0].Panel == "" {
		t.Fatalf("panel.html not discovered: %+v", packs)
	}
}

func TestPanelSizeCap(t *testing.T) {
	dir := t.TempDir()
	pd := filepath.Join(dir, "github")
	os.MkdirAll(filepath.Join(pd, "ui"), 0o755)
	writeFile(t, pd, "pack.yaml", testPackYAML)
	writeFile(t, pd, "git-status.rfx.yaml", validAliasFor("git-status"))
	writeFile(t, filepath.Join(pd, "ui"), "panel.html", strings.Repeat("x", MaxPanelBytes+1))

	l := NewLoader(dir, knownCore)
	if packs := l.Packs(); len(packs) != 1 || packs[0].Panel != "" {
		t.Fatalf("oversized panel should load the pack WITHOUT a panel: %+v", packs)
	}
	found := false
	for _, n := range l.Notices() {
		if strings.Contains(n, "panel.html") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no oversized-panel notice: %v", l.Notices())
	}
}

// A pack may declare panel capabilities (session read / turn post). They
// are opt-in per pack and surface to the host, which enforces them.
func TestPackUICapabilities(t *testing.T) {
	doc := testPackYAML + "ui:\n  session: true\n  turn: true\n  widgets: []\n"
	p, err := ParsePack([]byte(doc), "pack.yaml")
	if err != nil {
		t.Fatalf("ParsePack: %v", err)
	}
	if err := ValidatePack(p); err != nil {
		t.Fatalf("ValidatePack: %v", err)
	}
	if !p.UI.Session || !p.UI.Turn {
		t.Fatalf("capabilities not parsed: %+v", p.UI)
	}
}

// A pack with a panel but no reflexes is valid — a pure supervisor UI
// (e.g. planner) has nothing to run of its own.
func TestUIOnlyPackLoads(t *testing.T) {
	dir := t.TempDir()
	pd := filepath.Join(dir, "planner")
	os.MkdirAll(filepath.Join(pd, "ui"), 0o755)
	writeFile(t, pd, "pack.yaml", strings.Replace(testPackYAML, "pack: github", "pack: planner", 1)+
		"ui:\n  session: true\n  turn: true\n  widgets: []\n")
	writeFile(t, filepath.Join(pd, "ui"), "panel.html", "<div>planner</div>")

	l := NewLoader(dir, knownCore)
	packs := l.Packs()
	if len(packs) != 1 || packs[0].Panel == "" {
		t.Fatalf("ui-only pack not loaded: %+v", packs)
	}
	if len(l.All()) != 0 {
		t.Fatalf("expected zero reflexes, got %v", l.All())
	}
}
