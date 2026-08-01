package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cerveau/internal/rfx"
)

type stubTool struct {
	name   string
	schema map[string]any
	fn     func(args json.RawMessage, mode string) (string, error)
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub" }
func (s *stubTool) Schema() map[string]any {
	if s.schema != nil {
		return s.schema
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s *stubTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return s.fn(args, "")
}
func (s *stubTool) ExecuteMode(ctx context.Context, args json.RawMessage, mode string) (string, error) {
	return s.fn(args, mode)
}

func bashStep(cmd string) rfx.Step { return rfx.Step{Tool: "bash", Args: cmd} }

// testReg builds a registry with a no-op guard: the real Dispatch Guard is
// tested elsewhere; here we test the executor, and the registry fails closed
// for dangerous-tier tools without one.
func testReg(entries ...Entry) *Registry {
	r := NewRegistry(entries...)
	r.SetGuard(func(tool string, args json.RawMessage) error { return nil })
	return r
}

func pipeline(name string, steps ...rfx.Step) rfx.Reflex {
	return rfx.Reflex{
		RFX: 1, Name: name, Description: "test reflex " + name,
		Kind: rfx.KindPipeline, Risk: rfx.RiskDangerous,
		Steps: steps,
	}
}

func runReflex(t *testing.T, reg *Registry, name string, args map[string]any, mode string) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return reg.ExecuteMode(context.Background(), name, raw, mode)
}

func TestAliasExecutesThroughRegistry(t *testing.T) {
	var gotCmd string
	reg := testReg(Entry{Tool: &stubTool{name: "bash", fn: func(args json.RawMessage, mode string) (string, error) {
		var a struct {
			Command string `json:"command"`
		}
		json.Unmarshal(args, &a)
		gotCmd = a.Command
		return "build ok", nil
	}}, RiskTier: RiskDangerous, Modes: []string{ModeAutopilot}})

	if errs := reg.AddReflexes([]rfx.Reflex{pipeline("panel-build", bashStep("cd panel && npm run build"))}); len(errs) != 0 {
		t.Fatal(errs)
	}
	out, err := runReflex(t, reg, "panel-build", nil, ModeAutopilot)
	if err != nil {
		t.Fatal(err)
	}
	if gotCmd != "cd panel && npm run build" {
		t.Fatalf("bash got %q", gotCmd)
	}
	if !strings.Contains(out, "[ok]") || !strings.Contains(out, "build ok") {
		t.Fatalf("report missing step output: %q", out)
	}
}

func TestModeFencingPropagatesThroughReflex(t *testing.T) {
	var stepMode string
	reg := testReg(Entry{Tool: &stubTool{name: "bash", fn: func(args json.RawMessage, mode string) (string, error) {
		stepMode = mode
		return "ok", nil
	}}, RiskTier: RiskDangerous, Modes: []string{ModeAutopilot}})
	reg.AddReflexes([]rfx.Reflex{pipeline("do-it", bashStep("true"))})

	// Reflex invoked in discussion: the bash STEP must stay fenced out,
	// even though the reflex itself was callable.
	if _, err := runReflex(t, reg, "do-it", nil, ModeDiscussion); err == nil {
		t.Fatal("bash step ran in discussion mode — mode fencing bypassed")
	}
	if _, err := runReflex(t, reg, "do-it", nil, ModeAutopilot); err != nil {
		t.Fatalf("autopilot run failed: %v", err)
	}
	if stepMode != ModeAutopilot {
		t.Fatalf("step saw mode %q, want %q", stepMode, ModeAutopilot)
	}
}

func TestEmbeddedSubstitutionIsShellQuoted(t *testing.T) {
	var gotCmd string
	reg := testReg(Entry{Tool: &stubTool{name: "bash", fn: func(args json.RawMessage, mode string) (string, error) {
		var a struct {
			Command string `json:"command"`
		}
		json.Unmarshal(args, &a)
		gotCmd = a.Command
		return "ok", nil
	}}, RiskTier: RiskDangerous})

	def := pipeline("deploy", bashStep("rsync -a ./dist/ {{ params.target }}:/srv/"))
	def.Params = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{"type": "string"},
		},
		"required": []any{"target"},
	}
	reg.AddReflexes([]rfx.Reflex{def})

	// The classic injection payload must become inert quoted text.
	_, err := runReflex(t, reg, "deploy", map[string]any{"target": `host; rm -rf ~; #`}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "rsync -a ./dist/ 'host; rm -rf ~; #':/srv/"
	if gotCmd != want {
		t.Fatalf("injection not neutralized:\n got %q\nwant %q", gotCmd, want)
	}
}

func TestWholeFieldPlaceholderKeepsType(t *testing.T) {
	var gotVal any
	reg := testReg(Entry{Tool: &stubTool{name: "write", fn: func(args json.RawMessage, mode string) (string, error) {
		var a map[string]any
		json.Unmarshal(args, &a)
		gotVal = a["count"]
		return "ok", nil
	}}, RiskTier: RiskSensitive})

	step := rfx.Step{Tool: "write", Args: map[string]any{"path": "n.json", "count": "{{ params.count }}"}}
	def := pipeline("typed", step)
	def.Params = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"count"},
	}
	reg.AddReflexes([]rfx.Reflex{def})

	if _, err := runReflex(t, reg, "typed", map[string]any{"count": 42}, ""); err != nil {
		t.Fatal(err)
	}
	if gotVal != float64(42) {
		t.Fatalf("whole-field substitution stringified the value: %#v", gotVal)
	}
}

func TestStepOutputThreadsForward(t *testing.T) {
	var gotCmd string
	reg := testReg(
		Entry{Tool: &stubTool{name: "grep", fn: func(args json.RawMessage, mode string) (string, error) {
			return "src/main.go:42", nil
		}}, RiskTier: RiskSafe},
		Entry{Tool: &stubTool{name: "bash", fn: func(args json.RawMessage, mode string) (string, error) {
			var a struct {
				Command string `json:"command"`
			}
			json.Unmarshal(args, &a)
			gotCmd = a.Command
			return "ok", nil
		}}, RiskTier: RiskDangerous},
	)
	def := pipeline("chain",
		rfx.Step{ID: "find", Tool: "grep", Args: map[string]any{"pattern": "main"}},
		bashStep("wc -l {{ steps.find.output }}"),
	)
	reg.AddReflexes([]rfx.Reflex{def})

	if _, err := runReflex(t, reg, "chain", nil, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotCmd, "'src/main.go:42'") {
		t.Fatalf("step output not threaded (quoted) into bash: %q", gotCmd)
	}
}

func TestWhenSkipsAndOptionalContinues(t *testing.T) {
	ran := map[string]bool{}
	mk := func(name string, fail bool) Entry {
		return Entry{Tool: &stubTool{name: name, fn: func(args json.RawMessage, mode string) (string, error) {
			ran[name] = true
			if fail {
				return "boom output", &stubError{"it broke"}
			}
			return "fine", nil
		}}, RiskTier: RiskSafe}
	}
	reg := testReg(mk("t_fail", true), mk("t_a", false), mk("t_b", false))
	def := pipeline("flow",
		rfx.Step{ID: "first", Tool: "t_fail", Args: map[string]any{}, Optional: true},
		rfx.Step{ID: "second", Tool: "t_a", Args: map[string]any{}, When: "steps.first.ok"},
		rfx.Step{ID: "third", Tool: "t_b", Args: map[string]any{}, When: "steps.first.failed"},
	)
	reg.AddReflexes([]rfx.Reflex{def})

	out, err := runReflex(t, reg, "flow", nil, "")
	if err != nil {
		t.Fatalf("optional failure aborted the pipeline: %v", err)
	}
	if ran["t_a"] {
		t.Fatal("when steps.first.ok ran after a failure")
	}
	if !ran["t_b"] {
		t.Fatal("when steps.first.failed did not run after a failure")
	}
	if !strings.Contains(out, "[skipped") || !strings.Contains(out, "boom output") {
		t.Fatalf("report missing skip/failure detail: %q", out)
	}
}

type stubError struct{ msg string }

func (e *stubError) Error() string { return e.msg }

func TestHardFailureAbortsWithRealOutput(t *testing.T) {
	reg := testReg(Entry{Tool: &stubTool{name: "bash", fn: func(args json.RawMessage, mode string) (string, error) {
		return "compiler error on line 9", &stubError{"exit 1"}
	}}, RiskTier: RiskDangerous})
	def := pipeline("fragile", bashStep("make"), bashStep("make install"))
	reg.AddReflexes([]rfx.Reflex{def})

	report, err := runReflex(t, reg, "fragile", nil, "")
	if err == nil {
		t.Fatal("hard failure did not abort")
	}
	if !strings.Contains(report, "compiler error on line 9") {
		t.Fatalf("real step output not kept for self-correction: %q", report)
	}
}

func TestReflexDepthCap(t *testing.T) {
	reg := testReg()
	mk := func(name, calls string) rfx.Reflex {
		return pipeline(name, rfx.Step{Tool: calls, Args: map[string]any{}})
	}
	reg.AddReflexes([]rfx.Reflex{mk("ra", "rb"), mk("rb", "rc"), mk("rc", "ra")})

	_, err := runReflex(t, reg, "ra", nil, "")
	if err == nil || !strings.Contains(err.Error(), "max nesting depth") {
		t.Fatalf("cycle not stopped by depth cap: %v", err)
	}
}

func TestArgValidationCatchesGrammarBypass(t *testing.T) {
	reg := testReg(Entry{Tool: &stubTool{name: "bash", fn: func(args json.RawMessage, mode string) (string, error) {
		return "ok", nil
	}}, RiskTier: RiskDangerous})
	def := pipeline("strict", bashStep("echo {{ params.q }}"))
	def.Params = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"q": map[string]any{"type": "string", "enum": []any{"draft", "final"}},
		},
		"required": []any{"q"},
	}
	reg.AddReflexes([]rfx.Reflex{def})

	if _, err := runReflex(t, reg, "strict", map[string]any{}, ""); err == nil || !strings.Contains(err.Error(), "missing required") {
		t.Fatalf("missing required not caught: %v", err)
	}
	if _, err := runReflex(t, reg, "strict", map[string]any{"q": "yolo"}, ""); err == nil || !strings.Contains(err.Error(), "enum") {
		t.Fatalf("enum violation not caught: %v", err)
	}
	if _, err := runReflex(t, reg, "strict", map[string]any{"q": "draft", "zzz": 1}, ""); err == nil || !strings.Contains(err.Error(), "unknown param") {
		t.Fatalf("unknown param not caught: %v", err)
	}
	if _, err := runReflex(t, reg, "strict", map[string]any{"q": "final"}, ""); err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
}

func TestCardEnforcedAtDispatch(t *testing.T) {
	fetched := false
	reg := testReg(Entry{Tool: &stubTool{name: "web_fetch", fn: func(args json.RawMessage, mode string) (string, error) {
		fetched = true
		return "page text", nil
	}}, RiskTier: RiskSafe})
	def := pipeline("fetcher", rfx.Step{Tool: "web_fetch", Args: map[string]any{"url": "https://evil.example.com/"}})
	def.Card = rfx.Card{FS: []string{"workspace"}, Network: []string{"homelab.local"}}
	reg.AddReflexes([]rfx.Reflex{def})

	if _, err := runReflex(t, reg, "fetcher", nil, ""); err == nil || !strings.Contains(err.Error(), "card violation") {
		t.Fatalf("off-allowlist fetch not blocked: %v", err)
	}
	if fetched {
		t.Fatal("web_fetch executed despite card denial — check ran too late")
	}

	// Same reflex, allowlisted URL: passes.
	def.Steps = []rfx.Step{{Tool: "web_fetch", Args: map[string]any{"url": "http://homelab.local/t"}}}
	reg2 := testReg(Entry{Tool: &stubTool{name: "web_fetch", fn: func(args json.RawMessage, mode string) (string, error) {
		return "temps", nil
	}}, RiskTier: RiskSafe})
	reg2.AddReflexes([]rfx.Reflex{def})
	if _, err := runReflex(t, reg2, "fetcher", nil, ""); err != nil {
		t.Fatalf("allowlisted fetch blocked: %v", err)
	}
}

func TestReflexNamesModeFiltered(t *testing.T) {
	reg := testReg(Entry{Tool: &stubTool{name: "bash", fn: nil}, RiskTier: RiskDangerous})
	auto := pipeline("auto-only", bashStep("true"))
	auto.Modes = []string{ModeAutopilot}
	everywhere := pipeline("everywhere", bashStep("true"))
	reg.AddReflexes([]rfx.Reflex{auto, everywhere})

	if names := reg.ReflexNames(ModeAutopilot); len(names) != 2 {
		t.Fatalf("autopilot: %v, want both reflexes", names)
	}
	names := reg.ReflexNames(ModeDiscussion)
	if len(names) != 1 || names[0] != "everywhere" {
		t.Fatalf("discussion: %v, want [everywhere] only", names)
	}
	// Core tools never appear — only reflexes.
	for _, n := range reg.ReflexNames("") {
		if n == "bash" {
			t.Fatal("core tool leaked into ReflexNames")
		}
	}
}
