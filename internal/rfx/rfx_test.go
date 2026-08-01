package rfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func knownCore(name string) bool {
	switch name {
	case "bash", "read", "write", "edit", "grep", "web_fetch":
		return true
	}
	return false
}

const validAlias = `
rfx: 1
name: panel-build
description: Build the Svelte panel for production
risk: dangerous
modes: [autopilot]
kind: pipeline
steps:
  - bash: cd panel && npm run build
`

const validExec = `
rfx: 1
name: homelab-temps
description: Read current sensor temperatures from the homelab monitor
risk: safe
modes: [brainstorming, autopilot]
kind: exec
argv: [/opt/homelab/temps, --json]
timeout: 10s
card:
  network: [homelab.local]
  subprocess: true
params:
  type: object
  properties:
    zone: {type: string, enum: [attic, rack, desk]}
  required: [zone]
`

func parseValidate(t *testing.T, doc string) error {
	t.Helper()
	r, err := Parse([]byte(doc), "test.rfx.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Neutralize the stem rule here (TestFilenameStemRule covers it) so each
	// case fails for ITS reason, not the filename.
	r.Path = r.Name + ".rfx.yaml"
	return Validate(r, knownCore)
}

func TestValidAlias(t *testing.T) {
	if err := parseValidate(t, validAlias); err != nil {
		t.Fatalf("valid alias rejected: %v", err)
	}
}

func TestValidExec(t *testing.T) {
	if err := parseValidate(t, validExec); err != nil {
		t.Fatalf("valid exec rejected: %v", err)
	}
}

// Table: each invalid doc must be rejected, and for the reason named.
func TestRejections(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"version", strings.Replace(validAlias, "rfx: 1", "rfx: 2", 1), "rfx: must be 1"},
		{"name chars", strings.Replace(validAlias, "name: panel-build", "name: Panel_Build!", 1), "must match"},
		{"missing risk", strings.Replace(validAlias, "risk: dangerous\n", "", 1), "risk: required"},
		{"bad kind", strings.Replace(validAlias, "kind: pipeline", "kind: script", 1), "kind"},
		{"bad mode", strings.Replace(validAlias, "modes: [autopilot]", "modes: [yolo]", 1), "not one of"},
		{"bash can't be safe", strings.Replace(validAlias, "risk: dangerous", "risk: safe", 1), "may not declare safe"},
		{"unknown step tool", strings.Replace(validAlias, "bash:", "nmap:", 1), "unknown tool"},
		{"empty steps", strings.Replace(validAlias, "steps:\n  - bash: cd panel && npm run build\n", "steps: []\n", 1), "steps required"},
		{"exec needs argv", `
rfx: 1
name: homelab-temps
description: x
risk: safe
kind: exec
card: {subprocess: true}
`, "argv required"},
		{"exec argv0 absolute", strings.Replace(validExec, "/opt/homelab/temps", "temps", 1), "absolute path"},
		{"exec needs subprocess card", strings.Replace(validExec, "  subprocess: true\n", "", 1), "card.subprocess"},
		{"exec no steps", validExec + "steps:\n  - bash: echo hi\n", "only valid for kind pipeline"},
		{"unknown param placeholder", strings.Replace(validAlias, "npm run build", "npm run {{ params.flavor }}", 1), "not declared in params"},
		{"unknown step ref", strings.Replace(validAlias, "npm run build", "echo {{ steps.nope.output }}", 1), "no step with id"},
		{"malformed placeholder", strings.Replace(validAlias, "npm run build", "echo {{ paramx.a }}", 1), "malformed placeholder"},
		{"bad when syntax", `
rfx: 1
name: panel-build
description: x
risk: dangerous
kind: pipeline
steps:
  - id: a
    bash: "true"
  - bash: "true"
    when: steps.a.status == ok
`, "entire conditional language"},
		{"when forward ref", `
rfx: 1
name: panel-build
description: x
risk: dangerous
kind: pipeline
steps:
  - bash: "true"
    when: steps.later.ok
  - id: later
    bash: "true"
`, "previously defined"},
		{"dup step id", `
rfx: 1
name: panel-build
description: x
risk: dangerous
kind: pipeline
steps:
  - id: a
    bash: "true"
  - id: a
    bash: "true"
`, "duplicate step id"},
		{"params bad type", `
rfx: 1
name: panel-build
description: x
risk: dangerous
kind: pipeline
params:
  type: object
  properties:
    cb: {type: function}
steps:
  - bash: "true"
`, "not GBNF-compilable"},
		{"card fs relative", `
rfx: 1
name: panel-build
description: x
risk: dangerous
kind: pipeline
card: {fs: [../secrets]}
steps:
  - bash: "true"
`, "absolute path"},
		{"card network url", `
rfx: 1
name: panel-build
description: x
risk: dangerous
kind: pipeline
card: {network: ["https://evil.example/x"]}
steps:
  - bash: "true"
`, "host"},
		{"bad timeout", strings.Replace(validExec, "timeout: 10s", "timeout: soon", 1), "duration"},
		{"description too long", strings.Replace(validAlias, "Build the Svelte panel for production", strings.Repeat("x", 201), 1), "max 200"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := parseValidate(t, c.doc)
			if err == nil {
				t.Fatalf("accepted invalid manifest, want rejection containing %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("rejected but for wrong reason: got %q, want substring %q", err, c.want)
			}
		})
	}
}

func TestKnownFieldsRejectsTypos(t *testing.T) {
	doc := strings.Replace(validAlias, "description:", "descripton:", 1)
	if _, err := Parse([]byte(doc), "x.rfx.yaml"); err == nil {
		t.Fatal("typo'd field name silently accepted")
	}
}

func TestDefaults(t *testing.T) {
	r, err := Parse([]byte(validAlias), "panel-build.rfx.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if r.Cap() != 4000 {
		t.Fatalf("default cap = %d, want 4000", r.Cap())
	}
	if len(r.Card.FS) != 1 || r.Card.FS[0] != "workspace" {
		t.Fatalf("default card fs = %v, want [workspace]", r.Card.FS)
	}
	if len(r.Card.Network) != 1 || r.Card.Network[0] != "none" {
		t.Fatalf("default card network = %v, want [none]", r.Card.Network)
	}
	zero := 0
	r.IngressCap = &zero
	if r.Cap() != 0 {
		t.Fatal("explicit 0 cap must mean uncapped")
	}
}

func TestFilenameStemRule(t *testing.T) {
	r, _ := Parse([]byte(validAlias), "/home/u/.crv/rfx/other-name.rfx.yaml")
	err := Validate(r, knownCore)
	if err == nil || !strings.Contains(err.Error(), "filename stem") {
		t.Fatalf("stem mismatch not rejected: %v", err)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoaderDiscoveryAndErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "panel-build.rfx.yaml", validAlias)
	writeFile(t, dir, "homelab-temps.rfx.yaml", validExec)
	writeFile(t, dir, "broken.rfx.yaml", strings.Replace(validAlias, "name: panel-build", "name: broken", 1)+"\nbadfield: 1\n")
	writeFile(t, dir, "dup.rfx.yaml", validAlias)      // same name as panel-build → duplicate
	writeFile(t, dir, "not-a-reflex.yaml", validAlias) // wrong suffix: ignored entirely
	writeFile(t, dir, "README.md", "# hello")          // ignored

	l := NewLoader(dir, knownCore)
	got := l.List()
	if len(got) != 2 {
		names := []string{}
		for _, r := range got {
			names = append(names, r.Name)
		}
		t.Fatalf("List() = %v, want 2 valid reflexes", names)
	}
	errs := l.Errors()
	// broken.rfx.yaml (unknown field) + dup.rfx.yaml (stem mismatch: content
	// says panel-build, file says dup). NOTE: true name collisions are
	// unreachable while name==stem is enforced — DuplicateError stands ready
	// for v1.1 workspace-scoped reflexes, where collision becomes possible.
	if len(errs) != 2 {
		t.Fatalf("Errors() = %d, want 2: %v", len(errs), errs)
	}
	if _, ok := l.Get("homelab-temps"); !ok {
		t.Fatal("Get(homelab-temps) missing")
	}
}

func TestLoaderMissingDirIsNotAnError(t *testing.T) {
	l := NewLoader(filepath.Join(t.TempDir(), "does-not-exist"), knownCore)
	if got := l.List(); len(got) != 0 {
		t.Fatalf("missing dir: List() = %d, want 0", len(got))
	}
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("missing dir: Errors() = %v, want none (T0-friendly)", errs)
	}
}

func TestLoaderRescanPicksUpNewFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "panel-build.rfx.yaml", validAlias)
	l := NewLoader(dir, knownCore)
	if len(l.List()) != 1 {
		t.Fatal("initial scan wrong")
	}
	writeFile(t, dir, "homelab-temps.rfx.yaml", validExec)
	l.Scan() // forced; the 30s cache would hide it
	if len(l.List()) != 2 {
		t.Fatal("forced Scan() did not pick up new file")
	}
}
