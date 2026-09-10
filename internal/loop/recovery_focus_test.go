package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"cerveau/internal/episodic"
	"cerveau/internal/memory"
	"cerveau/internal/plan"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

func TestRecoveryLocationsOnlyDeclaredWorkspaceSource(t *testing.T) {
	ws := t.TempDir()
	step := PlanStep{Files: []string{"world.js", "tests.mjs", "private.txt", "../escape.js"}}
	evidence := fmt.Sprintf("file://%s/world.js:332\n at flush (file://%s/world.js:332:29)\n at test (file://%s/tests.mjs:299:5)\n at /etc/passwd:1\n at ../escape.js:4\n at undeclared.js:3\n at private.txt:1\n at file://remote%s/world.js:1\n at world.js:9999999999999999999\n", ws, ws, ws, ws)
	got := recoveryLocations(ws, step, evidence)
	want := []recoveryLocation{{"world.js", 332}, {"tests.mjs", 299}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations=%+v want=%+v", got, want)
	}
}

func TestRecoveryLocationsBoundAndDecodeFileURLs(t *testing.T) {
	step := PlanStep{Files: []string{"a space.js", "a.js", "b.js", "c.js"}}
	got := recoveryLocations("/work", step, "file:///work/a%20space.js:1\n a.js:30:1\n a.js:35:9\n b.js:1\n c.js:1\n")
	want := []recoveryLocation{{"a space.js", 1}, {"a.js", 30}, {"b.js", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations=%+v", got)
	}
}

func TestRecoveryFocusReadsOncePerSourceVersionAndRecordsRecall(t *testing.T) {
	m := newScriptedModel()
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	ws := t.TempDir()
	path := filepath.Join(ws, "world.js")
	if err := os.WriteFile(path, []byte("const oldState = {};\noldState.sky.set([]);\n"), 0600); err != nil {
		t.Fatal(err)
	}
	l.SetWorkspaceFunc(func(string) string { return ws })
	l.SetRecall(memory.NewRecall(nil, filepath.Dir(filepath.Dir(journal)), false))
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe})
	l.SetRegistry(reg)
	ctx, h, finish, err := l.beginRun(context.Background(), "s1", "autopilot", "")
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil, nil)
	h.registry = reg
	step := PlanStep{Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "command", Command: "test-script"}}
	p := restoreRecoveryProgress(nil, ws, "plan", 0, step)
	focus := newRecoveryFocus()
	v := Verdict{Evidence: "TypeError: missing sky\n at " + path + ":2:10", WorkspaceVersion: "v1"}
	first, err := l.focusRecovery(ctx, h.writer, "s1", reg, p, focus, step, v)
	if err != nil || !strings.Contains(first, "oldState.sky") || !strings.Contains(first, "sha256") {
		t.Fatalf("missing fresh source: %s %v", first, err)
	}
	second, err := l.focusRecovery(ctx, h.writer, "s1", reg, p, focus, step, v)
	if err != nil || !strings.Contains(second, "oldState.sky") {
		t.Fatalf("lost cached current-source evidence: %s %v", second, err)
	}
	count := func(kind string) int {
		events, err := episodic.Replay(journal)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, ev := range events {
			var payload struct{ Kind, Name string }
			_ = json.Unmarshal(ev.Payload, &payload)
			if (ev.Type == episodic.ToolCall && payload.Name == kind) || (ev.Type == episodic.Note && payload.Kind == kind) {
				n++
			}
		}
		return n
	}
	if count("read") != 1 || count("recovery_recall") != 1 {
		t.Fatal("unchanged check repeated read or retrieval")
	}
	if err := os.WriteFile(path, []byte("const newState = {};\nnewState.sky.set([]);\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v.WorkspaceVersion = "v2"
	third, err := l.focusRecovery(ctx, h.writer, "s1", reg, p, focus, step, v)
	if err != nil || !strings.Contains(third, "newState.sky") || strings.Contains(third, "oldState.sky") || count("read") != 2 || count("recovery_recall") != 2 {
		t.Fatalf("stale or missing focus: %s %v", third, err)
	}
	v.Pass = true
	if text, err := l.focusRecovery(ctx, h.writer, "s1", reg, p, focus, step, v); err != nil || text != "" || count("read") != 2 {
		t.Fatalf("successful check triggered recovery: %s %v", text, err)
	}
}

func TestRecoveryFocusRejectsEscapingSymlink(t *testing.T) {
	m := newScriptedModel()
	defer m.srv.Close()
	l, _ := gateFixture(t, m)
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private.js"), []byte("MUST_NOT_READ"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "private.js"), filepath.Join(ws, "world.js")); err != nil {
		t.Fatal(err)
	}
	step := PlanStep{Files: []string{"world.js"}}
	p := restoreRecoveryProgress(nil, ws, "plan", 0, step)
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe})
	text, err := l.focusRecovery(context.Background(), nil, "s1", reg, p, newRecoveryFocus(), step, Verdict{Evidence: "at world.js:1"})
	if err != nil || strings.Contains(text, "MUST_NOT_READ") || len(p.entries) != 0 {
		t.Fatalf("escaped workspace: %s %v", text, err)
	}
}

func TestRecoveryFocusBoundsDenseSourceForSmallContexts(t *testing.T) {
	m := newScriptedModel()
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	ws := t.TempDir()
	step := PlanStep{Files: []string{"a.js", "b.js", "c.js"}}
	for _, name := range step.Files {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(strings.Repeat("// 界 wide line\n", 1200)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	l.SetWorkspaceFunc(func(string) string { return ws })
	l.SetRecall(memory.NewRecall(nil, filepath.Dir(filepath.Dir(journal)), false))
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe})
	l.SetRegistry(reg)
	ctx, h, finish, err := l.beginRun(context.Background(), "s1", "autopilot", "")
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil, nil)
	h.registry = reg
	progress := restoreRecoveryProgress(nil, ws, "plan", 0, step)
	focus := newRecoveryFocus()
	v := Verdict{Evidence: "TypeError: state missing\n at a.js:3\n at b.js:3\n at c.js:3"}
	for _, budget := range []int{8192, 32768, 256} {
		l.win = window.NewManager(budget, 2048, nil)
		text, err := l.focusRecovery(ctx, h.writer, "s1", reg, progress, focus, step, v)
		if err != nil || len(text) > l.recoveryFocusBudget() || !utf8.ValidString(text) {
			t.Fatalf("context %d: oversized/invalid focus %d bytes: %v", budget, len(text), err)
		}
		if budget == 8192 && !strings.Contains(text, "DISPLAY EXCERPT ONLY") {
			t.Fatal("limited context pretended to show complete source receipts")
		}
	}
	events, err := episodic.Replay(journal)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	for _, event := range events {
		var call struct{ Name string }
		_ = json.Unmarshal(event.Payload, &call)
		if event.Type == episodic.ToolCall && call.Name == "read" {
			reads++
		}
	}
	if reads != 3 {
		t.Fatalf("changed context budget re-read unchanged source: %d reads", reads)
	}
}

type recoveryFocusCheck struct{ path string }

func (t recoveryFocusCheck) Name() string        { return "bash" }
func (t recoveryFocusCheck) Description() string { return "fixture check" }
func (t recoveryFocusCheck) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}}
}
func (t recoveryFocusCheck) Execute(context.Context, json.RawMessage) (string, error) {
	data, err := os.ReadFile(t.path)
	if err != nil {
		return "", err
	}
	if strings.Contains(string(data), "initialized") {
		return "fixture behavior verified", nil
	}
	return "TypeError: missing state\n at " + t.path + ":2:1", fmt.Errorf("fixture failed")
}

func TestRecoveryFirstModelCallReceivesFailureSourceAndCanResume(t *testing.T) {
	m := newScriptedModel(toolCall("edit", `{"path":"world.js","old_string":"missing","new_string":"initialized"}`), textReply("repair ready"))
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	ws := t.TempDir()
	path := filepath.Join(ws, "world.js")
	if err := os.WriteFile(path, []byte("// fixture state\nconst state = 'missing';\n"), 0600); err != nil {
		t.Fatal(err)
	}
	l.SetWorkspaceFunc(func(string) string { return ws })
	l.SetRegistry(tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewEdit(ws), RiskTier: tools.RiskSensitive},
		tools.Entry{Tool: recoveryFocusCheck{path}, RiskTier: tools.RiskSafe},
	))
	w, err := episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(episodic.Plan, &Plan{Title: "recovery fixture", Steps: []PlanStep{{Title: "state", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "command", Command: "fixture-check"}}}})
	w.Append(episodic.Checkpoint, map[string]any{"index": 0, "status": "blocked", "evidence": "previous failure"})
	w.Close()
	result, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1, Continue: true})
	if err != nil || result.StopReason == "plan_blocked" {
		t.Fatalf("recovery failed: %+v %v", result, err)
	}
	if len(m.bodies) == 0 || !strings.Contains(m.bodies[0], "FAILURE-FOCUSED CONTEXT") || !strings.Contains(m.bodies[0], "const state = 'missing'") || !strings.Contains(m.bodies[0], "sha256") {
		t.Fatal("model had to rediscover current failing source")
	}
	state, err := l.PlanStateOf("s1")
	if err != nil || !state.Done {
		t.Fatalf("verified repair did not resume: %+v %v", state, err)
	}
}
