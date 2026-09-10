package loop

import (
	"cerveau/internal/episodic"
	"cerveau/internal/tools"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryCopiesAreVerifiedAndDoNotRestoreAutomatically(t *testing.T) {
	ws, archive := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(ws, "world.js"), []byte("complete source"), 0600)
	p := &Plan{Steps: []PlanStep{{Files: []string{"world.js", "missing.js", "../outside"}}}}
	copies, err := preservePlanFiles(ws, archive, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(copies) != 3 {
		t.Fatalf("missing explicit coverage: %+v", copies)
	}
	os.WriteFile(filepath.Join(ws, "world.js"), []byte("broken"), 0600)
	data, err := readRecoveryCopy(archive, copies[0])
	if err != nil || string(data) != "complete source" {
		t.Fatalf("copy: %q %v", data, err)
	}
	os.WriteFile(filepath.Join(archive, copies[0].Blob), []byte("corrupt"), 0600)
	if _, err := readRecoveryCopy(archive, copies[0]); err == nil {
		t.Fatal("corrupt copy accepted")
	}
	got, _ := os.ReadFile(filepath.Join(ws, "world.js"))
	if string(got) != "broken" {
		t.Fatal("implicit restore")
	}
}

func TestRecoveryRepairsRechecksAndContinues(t *testing.T) {
	// This fixture tests the existing content checks/recovery lifecycle, not
	// parser damage. Keep its marker mutations valid JavaScript now that every
	// structured JS write is syntax guarded; the checks below remain unchanged.
	m := newScriptedModel(textReply("foundation"), toolCall("write", `{"path":"world.js","content":"// base broken\n"}`), textReply("failed"), toolCall("write", `{"path":"world.js","content":"// base repaired\n"}`), textReply("repaired"), textReply("last step"))
	defer m.srv.Close()
	l, path := gateFixture(t, m)
	ws := l.workspace("s1")
	os.WriteFile(filepath.Join(ws, "world.js"), []byte("// base\n"), 0600)
	l.SetRegistry(tools.NewRegistry(append(l.registry().Entries(), tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSensitive})...))
	wr, _ := episodic.Open(path)
	wr.Append(episodic.Plan, map[string]any{"title": "recovery fixture", "steps": []map[string]any{
		{"title": "foundation", "files": []string{"world.js"}, "verify": map[string]string{"kind": "contains", "file": "world.js", "symbol": "base"}},
		{"title": "mesh", "files": []string{"world.js"}, "verify": map[string]string{"kind": "contains", "file": "world.js", "symbol": "repaired"}},
		{"title": "integration", "files": []string{"world.js"}, "verify": map[string]string{"kind": "contains", "file": "world.js", "symbol": "base repaired"}},
	}})
	wr.Close()
	result, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	state, err := l.PlanStateOf("s1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Done || state.Steps[1].Attempts != 2 {
		t.Fatalf("result %+v state %+v", result, state)
	}
	m.mu.Lock()
	bodies := append([]string(nil), m.bodies...)
	offered := append([][]string(nil), m.offered...)
	m.mu.Unlock()
	if len(bodies) != 6 || !strings.Contains(bodies[3], "RECOVERY") || !strings.Contains(bodies[3], "base broken") {
		t.Fatalf("retry not grounded: %v", bodies)
	}
	if !strings.Contains(strings.Join(offered[3], ","), "recovery_read") || strings.Contains(strings.Join(offered[5], ","), "recovery_read") {
		t.Fatal("recovery reader scope leaked")
	}
	events, _ := episodic.Replay(path)
	checks := 0
	stale := false
	repairing := false
	for _, e := range events {
		if e.Type == episodic.Note {
			var n struct {
				Kind  string
				Index int
			}
			json.Unmarshal(e.Payload, &n)
			if n.Kind == "verify_finished" && n.Index == 0 {
				checks++
			}
		}
		if e.Type == episodic.PlanState && strings.Contains(string(e.Payload), "needs_reverify") {
			stale = true
		}
		if e.Type == episodic.RunState && strings.Contains(string(e.Payload), `"recovery_phase":"repairing"`) {
			repairing = true
		}
	}
	if checks < 2 || !stale || !repairing {
		t.Fatalf("missing recovery evidence: checks=%d stale=%v repairing=%v", checks, stale, repairing)
	}
}

func TestRecoveryExplicitContinuation(t *testing.T) {
	l, _, close := stepRunFixture(t)
	defer close()
	if _, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: 0, Continue: true}); err != nil {
		t.Fatal(err)
	}
	state, _ := l.PlanStateOf("s1")
	if !state.Done {
		t.Fatal("explicit continuation stopped after first step")
	}
}

func TestRecoveryStopsWhenEarlierCheckBreaks(t *testing.T) {
	m := newScriptedModel(textReply("base"), toolCall("write", `{"path":"world.js","content":"mesh"}`), textReply("mesh ready"), textReply("must not run"))
	defer m.srv.Close()
	l, path := gateFixture(t, m)
	ws := l.workspace("s1")
	os.WriteFile(filepath.Join(ws, "world.js"), []byte("base"), 0600)
	l.SetRegistry(tools.NewRegistry(append(l.registry().Entries(), tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSensitive})...))
	wr, _ := episodic.Open(path)
	wr.Append(episodic.Plan, map[string]any{"title": "shared source", "steps": []map[string]any{
		{"title": "base", "files": []string{"world.js"}, "verify": map[string]string{"kind": "contains", "file": "world.js", "symbol": "base"}},
		{"title": "mesh", "files": []string{"world.js"}, "verify": map[string]string{"kind": "contains", "file": "world.js", "symbol": "mesh"}},
		{"title": "later", "files": []string{"later.js"}, "verify": map[string]string{"kind": "contains", "file": "world.js", "symbol": "mesh"}},
	}})
	wr.Close()
	result, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := l.PlanStateOf("s1")
	if result.StopReason != "plan_blocked" || state.Steps[0].Status != "blocked" || state.Steps[2].Attempts != 0 {
		t.Fatalf("continued on broken foundation: %+v %+v", result, state)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bodies) != 3 {
		t.Fatalf("unexpected model calls: %d", len(m.bodies))
	}
}

func TestRecoveryHistoryPlanScopeAndRestart(t *testing.T) {
	ws, archive := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(ws, "a.js"), []byte("original"), 0600)
	copies, err := preservePlanFiles(ws, archive, &Plan{Steps: []PlanStep{{Files: []string{"a.js"}}}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"kind": "recovery_snapshot", "copies": copies})
	events := []episodic.Event{{Type: episodic.Plan}, {Type: episodic.Note, Payload: payload}}
	_, reader := recoveryHistory(events, archive)
	args, _ := json.Marshal(map[string]string{"id": copies[0].Blob})
	out, err := reader.Execute(context.Background(), args)
	if err != nil || !strings.Contains(out, "original") {
		t.Fatalf("restart read: %s %v", out, err)
	}
	events = append(events, episodic.Event{Type: episodic.Plan})
	_, reader = recoveryHistory(events, archive)
	if _, err = reader.Execute(context.Background(), args); err == nil {
		t.Fatal("old plan snapshot leaked")
	}
}

func TestRecoveryStaleEvidenceRechecksWithoutModel(t *testing.T) {
	l, _, close := stepRunFixture(t)
	defer close()
	if _, err := l.RunAutopilot(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	events, err := episodic.Replay(l.path("s1"))
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := ReducePlan(events)
	if err != nil {
		t.Fatal(err)
	}
	s.Steps[0].Status = "needs_reverify"
	wr, _ := episodic.Open(l.path("s1"))
	if err = l.saveSupervisor(wr, "s1", s); err != nil {
		t.Fatal(err)
	}
	wr.Close()
	result, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Iterations != 0 {
		t.Fatalf("stale check rebuilt via model: %+v", result)
	}
	state, _ := l.PlanStateOf("s1")
	if !state.Done {
		t.Fatal("recheck failed to restore current evidence")
	}
}

func TestRecoverySelectedDoesNotRecheckUnselectedFoundation(t *testing.T) {
	l, _, close := stepRunFixture(t)
	defer close()
	p, id, err := LatestPlan(l.path("s1"))
	if err != nil {
		t.Fatal(err)
	}
	p.Steps[1].Files = []string{"a.js"}
	wr, _ := episodic.Open(l.path("s1"))
	ev, err := wr.Append(episodic.Plan, p)
	wr.Close()
	if err != nil {
		t.Fatal(err)
	}
	id = ev.ID
	if _, err = l.RunStep(context.Background(), "s1", StepRunRequest{Step: 0, PlanID: id}); err != nil {
		t.Fatal(err)
	}
	result, err := l.RunSelected(context.Background(), "s1", id, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	state, _ := l.PlanStateOf("s1")
	if result.StopReason != "plan_blocked" || state.Steps[0].Status != "needs_reverify" {
		t.Fatalf("unselected check was silently widened: %+v %+v", result, state)
	}
	result, err = l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	state, _ = l.PlanStateOf("s1")
	if !state.Done || result.Iterations != 0 {
		t.Fatal("whole-plan recheck rebuilt completed work")
	}
}

func TestRecoveryBriefKeepsFailureTail(t *testing.T) {
	s := NewSupervisor(&Plan{Steps: []PlanStep{{Title: "mesh"}}})
	s.Steps[0].Verdict = &Verdict{Check: "node tests.mjs", Evidence: strings.Repeat("x", 1800) + "SyntaxError: Unexpected end of input"}
	brief := recoveryInstructions(s, 0)
	if !strings.Contains(brief, "SyntaxError: Unexpected end of input") || !strings.Contains(brief, "RECOVERY") {
		t.Fatal(brief)
	}
}

func TestRecoveryTargetPrefersFailureOverStaleEvidence(t *testing.T) {
	s := NewSupervisor(&Plan{Steps: []PlanStep{{Title: "base"}, {Title: "mesh"}}})
	s.Steps[0].Status = "needs_reverify"
	s.Steps[1].Status = "failed"
	s.Steps[1].Verdict = &Verdict{Evidence: "syntax error"}
	if recoveryTarget(s) != 1 {
		t.Fatal("retry selected a stale prerequisite instead of the failure")
	}
	s.Steps[1].Status = "pending"
	s.Steps[1].Verdict = nil
	s.Steps[1].Reason = "Execution interrupted; inspect existing effects before retrying."
	if recoveryTarget(s) != 1 {
		t.Fatal("interrupted active step lost")
	}
}
