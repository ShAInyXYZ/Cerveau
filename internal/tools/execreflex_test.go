package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cerveau/internal/rfx"
)

func execDef(name string, argv ...string) rfx.Reflex {
	return rfx.Reflex{
		RFX: 1, Name: name, Description: "test exec " + name,
		Kind: rfx.KindExec, Risk: rfx.RiskSafe,
		Argv: argv,
		Card: rfx.Card{FS: []string{"workspace"}, Network: []string{"none"}, Subprocess: true},
	}
}

func runExec(t *testing.T, def rfx.Reflex, args map[string]any) (string, error) {
	t.Helper()
	reg := testReg()
	reg.SetWorkspace(t.TempDir())
	if errs := reg.AddReflexes([]rfx.Reflex{def}); len(errs) != 0 {
		t.Fatal(errs)
	}
	raw, _ := json.Marshal(args)
	return reg.ExecuteMode(context.Background(), def.Name, raw, "")
}

func TestExecJSONRoundtrip(t *testing.T) {
	// /bin/cat echoes stdin: the args JSON must arrive on stdin verbatim.
	def := execDef("echo-args", "/bin/cat")
	def.Params = map[string]any{
		"type":       "object",
		"properties": map[string]any{"zone": map[string]any{"type": "string", "enum": []any{"attic", "rack"}}},
		"required":   []any{"zone"},
	}
	out, err := runExec(t, def, map[string]any{"zone": "rack"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"zone":"rack"`) {
		t.Fatalf("stdin JSON not delivered: %q", out)
	}
}

func TestExecJSONOutputFieldWins(t *testing.T) {
	def := execDef("json-out", "/usr/bin/printf", `{"output":"42 degrees","ignored":"x"}`)
	out, err := runExec(t, def, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "42 degrees" {
		t.Fatalf("output field not extracted: %q", out)
	}
}

func TestExecArgvSubstitutionLiteral(t *testing.T) {
	def := execDef("lit", "/usr/bin/printf", "{{ params.msg }}")
	def.Params = map[string]any{
		"type":       "object",
		"properties": map[string]any{"msg": map[string]any{"type": "string"}},
		"required":   []any{"msg"},
	}
	// No shell → the payload prints literally; no quoting needed, none applied.
	out, err := runExec(t, def, map[string]any{"msg": "a; b $HOME `x`"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "a; b $HOME `x`" {
		t.Fatalf("argv substitution not literal: %q", out)
	}
}

func TestExecEnvScrubbed(t *testing.T) {
	t.Setenv("RFX_SECRET_MARKER", "should-not-leak")
	def := execDef("envs", "/usr/bin/env")
	out, err := runExec(t, def, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "RFX_SECRET_MARKER") {
		t.Fatal("ambient env leaked into exec subprocess")
	}
	if !strings.Contains(out, "PATH=") || !strings.Contains(out, "HOME=") {
		t.Fatalf("base env missing: %q", out)
	}
	// Card-allowlisted name DOES pass.
	def2 := execDef("envs2", "/usr/bin/env")
	def2.Card.Env = []string{"RFX_SECRET_MARKER"}
	reg := testReg()
	reg.SetWorkspace(t.TempDir())
	reg.AddReflexes([]rfx.Reflex{def2})
	out2, err := reg.ExecuteMode(context.Background(), "envs2", json.RawMessage(`{}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "RFX_SECRET_MARKER=should-not-leak") {
		t.Fatalf("card env allowlist not honored: %q", out2)
	}
}

func TestExecFailureKeepsStderr(t *testing.T) {
	def := execDef("fails", "/bin/sh", "-c", "echo specific-reason >&2; exit 3")
	out, err := runExec(t, def, nil)
	if err == nil {
		t.Fatal("exit 3 not an error")
	}
	if !strings.Contains(out, "specific-reason") {
		t.Fatalf("stderr not kept for self-correction: %q", out)
	}
}

func TestExecTimeoutKillsTree(t *testing.T) {
	def := execDef("sleeps", "/bin/sleep", "60")
	def.Timeout = "100ms"
	start := time.Now()
	_, err := runExec(t, def, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout not enforced: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("killed process still held the call")
	}
}

func TestExecRegisteredAndCollisions(t *testing.T) {
	reg := testReg(Entry{Tool: &stubTool{name: "bash", fn: nil}, RiskTier: RiskDangerous})
	reg.SetWorkspace(t.TempDir())
	errs := reg.AddReflexes([]rfx.Reflex{
		pipeline("bash", bashStep("true")),
		execDef("ext", "/bin/true"),
	})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "collides") {
		t.Fatalf("collision not rejected: %v", errs)
	}
	if _, exists := reg.Entry("ext"); !exists {
		t.Fatal("exec reflex not registered (Synapse should have landed)")
	}
	// No workspace → exec registration refuses loudly.
	reg2 := testReg()
	if errs := reg2.AddReflexes([]rfx.Reflex{execDef("ext2", "/bin/true")}); len(errs) != 1 {
		t.Fatalf("missing workspace not caught: %v", errs)
	}
}
