package tools

import (
	"context"
	"strings"
	"testing"

	"cerveau/internal/rfx"
)

func TestFuzzReflexDryRunGreen(t *testing.T) {
	def := pipeline("fz", bashStep("echo {{ params.word }}"))
	def.Params = map[string]any{
		"type":       "object",
		"properties": map[string]any{"word": map[string]any{"type": "string", "enum": []any{"a", "b"}}},
		"required":   []any{"word"},
	}
	def.Contract = rfx.Contract{MaxMs: 60000, MustNotContain: []string{"PANIC-MARKER"}}

	rep := FuzzReflex(context.Background(), def, rfx.GenerateArgs(def.Params, 100))
	if fails := rep.Failures(); len(fails) != 0 {
		t.Fatalf("green reflex failed fuzz: %v", fails)
	}
	if len(rep.Runs) < 2 {
		t.Fatalf("expected ≥2 runs (2 enum values), got %d", len(rep.Runs))
	}
	// Dry-run must be side-effect free: outputs come from the stub.
	if !strings.Contains(rep.Runs[0].Output, "DRY-RUN") {
		t.Fatalf("real tool executed during fuzz? output: %q", rep.Runs[0].Output)
	}
}

func TestFuzzReflexCatchesContractViolation(t *testing.T) {
	def := pipeline("bad", bashStep("echo hi"))
	def.Contract = rfx.Contract{OutputRegex: "^DEPLOYED"} // dry-run output will never match
	rep := FuzzReflex(context.Background(), def, rfx.GenerateArgs(nil, 100))
	if fails := rep.Failures(); len(fails) == 0 {
		t.Fatal("contract violation not caught by fuzz")
	}
}

func TestFuzzReflexCatchesBrokenParams(t *testing.T) {
	// Param used in the step but undeclared in params → substitution error
	// must surface as a fuzz failure, not a 2 AM autopilot surprise.
	def := pipeline("broken", bashStep("echo {{ params.missing }}"))
	def.Params = map[string]any{
		"type":       "object",
		"properties": map[string]any{"present": map[string]any{"type": "string"}},
	}
	rep := FuzzReflex(context.Background(), def, rfx.GenerateArgs(def.Params, 100))
	if fails := rep.Failures(); len(fails) == 0 {
		t.Fatal("undeclared param placeholder not caught by fuzz")
	}
}

func TestFuzzReflexExecRunsForReal(t *testing.T) {
	// Exec fuzz is LIVE by design: a stubbed exec proves nothing. The binary
	// existing + honoring the output contract IS the test.
	def := execDef("fz-exec", "/usr/bin/printf", `{"output":"load average: ok"}`)
	def.Contract = rfx.Contract{OutputRegex: "load average"}
	rep := FuzzReflex(context.Background(), def, rfx.GenerateArgs(nil, 100))
	if fails := rep.Failures(); len(fails) != 0 {
		t.Fatalf("real exec fuzz failed: %v", fails)
	}
	if rep.Runs[0].Output != "load average: ok" {
		t.Fatalf("exec output not parsed per spec: %q", rep.Runs[0].Output)
	}

	// Missing binary must fail AT FUZZ, not in production.
	def2 := execDef("fz-missing", "/nonexistent/binary")
	rep2 := FuzzReflex(context.Background(), def2, rfx.GenerateArgs(nil, 100))
	if fails := rep2.Failures(); len(fails) == 0 {
		t.Fatal("missing binary not caught by fuzz")
	}
}
