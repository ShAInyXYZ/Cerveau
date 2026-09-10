package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
	"cerveau/internal/tools"
)

func recoverySharedFixture(t *testing.T, m *scriptedModel) (*Loop, string, string) {
	t.Helper()
	l, journal := gateFixture(t, m)
	ws := t.TempDir()
	l.SetWorkspaceFunc(func(string) string { return ws })
	if err := os.WriteFile(filepath.Join(ws, "world.js"), []byte("BASE MESH"), 0600); err != nil {
		t.Fatal(err)
	}
	l.SetRegistry(tools.NewRegistry(tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSafe}))
	w, err := episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	ev, err := w.Append(episodic.Plan, &Plan{Title: "shared source recovery", Steps: []PlanStep{
		{Title: "base", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "BASE"}},
		{Title: "lighting", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "LIGHT"}},
		{Title: "mesh", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "MESH"}},
		{Title: "following", Files: []string{"next.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "BASE"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2} {
		w.Append(episodic.Checkpoint, map[string]any{"index": i, "status": "passed", "evidence": "previous check passed"})
	}
	w.Append(episodic.Checkpoint, map[string]any{"index": 1, "status": "failed", "evidence": "lighting failed"})
	return l, journal, ev.ID
}

func TestRecoveryInvalidatesPassedSharedStepsOnBothSidesOnly(t *testing.T) {
	p := &Plan{Steps: []PlanStep{
		{Files: []string{"world.js"}},
		{Files: []string{"world.js"}},
		{Files: []string{"./world.js"}},
		{Files: []string{"unrelated.js"}},
	}}
	s := NewSupervisor(p)
	for i := range s.Steps {
		s.Steps[i].Status = "passed"
	}
	s.Steps[1].Status = "running"
	if got := invalidateSharedEvidence(s, 1); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("stale=%v", got)
	}
	if s.Steps[1].Status != "running" || s.Steps[3].Status != "passed" {
		t.Fatal("active/unrelated step invalidated", s.Steps)
	}
	// A not-yet-started downstream step has no passing evidence to invalidate.
	s.Steps[2].Status = "pending"
	if got := invalidateSharedEvidence(s, 1); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("pending step invalidated: %v", got)
	}
}

func TestRecoveryRepairRechecksAndBlocksBrokenDownstreamPass(t *testing.T) {
	m := newScriptedModel(toolCall("write", `{"path":"world.js","content":"BASE LIGHT"}`), textReply("lighting repaired"), textReply("following must not run"))
	defer m.srv.Close()
	l, _, _ := recoverySharedFixture(t, m)
	result, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1, Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	st, err := l.PlanStateOf("s1")
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "plan_blocked" || st.Steps[0].Status != "passed" || st.Steps[1].Status != "passed" || st.Steps[2].Status != "blocked" || st.Steps[3].Attempts != 0 {
		t.Fatalf("continued over stale downstream pass: %+v %+v", result, st.Steps)
	}
	if st.Blocked != 2 || len(m.bodies) != 2 {
		t.Fatalf("wrong blocker or extra execution: blocked=%d calls=%d", st.Blocked, len(m.bodies))
	}
	events, _ := episodic.Replay(l.path("s1"))
	sup, _, _ := ReducePlan(events)
	if recoveryTarget(sup) != 2 {
		t.Fatal("retry did not target newly broken downstream step")
	}
}

func TestSelectedRecoveryLeavesUnselectedSharedPassStaleWithoutCheckingIt(t *testing.T) {
	m := newScriptedModel(toolCall("write", `{"path":"world.js","content":"BASE LIGHT"}`), textReply("lighting repaired"))
	defer m.srv.Close()
	l, journal, planID := recoverySharedFixture(t, m)
	result, err := l.RunSelected(context.Background(), "s1", planID, []int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := l.PlanStateOf("s1")
	if result.StopReason != "plan_blocked" || st.Steps[2].Status != "needs_reverify" || st.Steps[3].Attempts != 0 {
		t.Fatalf("unselected stale work lost: %+v %+v", result, st.Steps)
	}
	events, _ := episodic.Replay(journal)
	for _, ev := range events {
		if ev.Type != episodic.Note {
			continue
		}
		var n struct {
			Kind  string
			Index int
		}
		if json.Unmarshal(ev.Payload, &n) == nil && n.Kind == "verify_finished" && n.Index == 2 {
			t.Fatal("verified outside selected scope")
		}
	}
}
