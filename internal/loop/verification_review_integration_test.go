package loop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
	"cerveau/internal/tools"
)

func TestVerificationReviewStopsWithoutChangingCheckOrRunningProposal(t *testing.T) {
	m := newScriptedModel(textReply("must not run"))
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "mesh.txt"), []byte("256"), 0600); err != nil {
		t.Fatal(err)
	}
	l.SetWorkspaceFunc(func(string) string { return ws })
	l.SetRegistry(tools.NewRegistry(tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe}, tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSafe}))
	w, err := episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	original := &Plan{Title: "mesh", Steps: []PlanStep{
		{Title: "mesh", Files: []string{"mesh.txt"}, Verify: &plan.Verify{Kind: "contains", File: "mesh.txt", Symbol: "300"}},
		{Title: "next", Files: []string{"next.txt"}, Verify: &plan.Verify{Kind: "contains", File: "mesh.txt", Symbol: "256"}},
	}}
	pe, err := w.Append(episodic.Plan, original)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(episodic.ToolCall, map[string]any{"id": "prior-read", "name": "read", "args": map[string]any{"path": "mesh.txt"}, "run_id": "prior", "session_id": "s1", "plan_event_id": pe.ID})
	evidence, err := w.Append(episodic.ToolResult, map[string]any{"id": "prior-read", "name": "read", "ok": true, "output": "256", "run_id": "prior", "session_id": "s1", "plan_event_id": pe.ID})
	if err != nil {
		t.Fatal(err)
	}
	w.Append(episodic.Checkpoint, map[string]any{"index": 0, "status": "blocked", "evidence": "expected 300"})
	w.Close()
	args, _ := json.Marshal(map[string]any{"reason": "The generated threshold contradicts observed flat fixture coverage.", "evidence_event_ids": []string{evidence.ID}, "proposed_verify": map[string]any{"kind": "command", "command": "node -e \"require('fs').writeFileSync('SHOULD_NOT_RUN','bad')\""}})
	reviewReply := toolCall("request_verification_review", string(args))
	message := reviewReply["message"].(map[string]any)
	message["tool_calls"] = append(message["tool_calls"].([]map[string]any), map[string]any{
		"id": "must-not-write", "type": "function", "function": map[string]any{"name": "write", "arguments": `{"path":"SHOULD_NOT_RUN","content":"bad"}`},
	})
	m.replies = []map[string]any{reviewReply, textReply("must not continue")}
	result, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1, Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "plan_blocked" || len(m.bodies) != 1 {
		t.Fatalf("review failed to stop immediately: calls=%d result=%+v", len(m.bodies), result)
	}
	if !strings.Contains(result.Reply, "Verification review requested") || !strings.Contains(result.Reply, "not applied") {
		t.Fatal(result.Reply)
	}
	state, err := l.PlanStateOf("s1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Steps[0].Status != "blocked" || state.Steps[0].Attempts != 1 || state.Steps[0].Verdict.Pass || state.Steps[1].Attempts != 0 {
		t.Fatalf("review advanced plan: %+v", state.Steps)
	}
	raw, _ := json.Marshal(state.Steps[0].Verdict)
	var verdict map[string]json.RawMessage
	json.Unmarshal(raw, &verdict)
	if len(verdict["verification_review"]) == 0 {
		t.Fatal("proposal missing after replay", string(raw))
	}
	current, id, err := LatestPlan(journal)
	if err != nil || id != pe.ID || current.Steps[0].Verify.Symbol != "300" {
		t.Fatalf("original contract changed: %s %+v %v", id, current, err)
	}
	if body, _ := os.ReadFile(filepath.Join(ws, "mesh.txt")); string(body) != "256" {
		t.Fatal("benchmark bytes changed")
	}
	if _, err := os.Stat(filepath.Join(ws, "SHOULD_NOT_RUN")); !os.IsNotExist(err) {
		t.Fatal("proposal command executed")
	}
	if !hasTool(m.offered[0], "read_plan_step") {
		t.Fatal("exact plan context tool missing")
	}
	// A generic retry is a new attempt under the SAME check, not approval of
	// the proposed command, and carries the review back into the model window.
	_, err = l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1, Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	current, id, err = LatestPlan(journal)
	if err != nil || id != pe.ID || current.Steps[0].Verify.Symbol != "300" {
		t.Fatal("retry replaced the original criterion")
	}
	if _, err := os.Stat(filepath.Join(ws, "SHOULD_NOT_RUN")); !os.IsNotExist(err) {
		t.Fatal("retry executed the proposal or remaining batch")
	}
	if len(m.bodies) < 2 || !strings.Contains(m.bodies[1], "Retry is not approval") {
		t.Fatal("retry lost the recorded review")
	}
}

func TestStepRunExposesExactSavedPlanContract(t *testing.T) {
	m := newScriptedModel(toolCall("read_plan_step", `{"index":0}`), textReply("observed"))
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	w, _ := episodic.Open(journal)
	const needle = "exact verification must remain available after the clipped report label"
	w.Append(episodic.Plan, &Plan{Title: "context", Steps: []PlanStep{{Title: "one", Files: []string{"index.html"}, Verify: &plan.Verify{Kind: "contains", File: "index.html", Symbol: needle}}}})
	w.Close()
	l.RunStep(context.Background(), "s1", StepRunRequest{Step: 0})
	events, _ := episodic.Replay(journal)
	found := false
	for _, e := range events {
		if e.Type != episodic.ToolResult {
			continue
		}
		var p struct {
			Name, Output string
			OK           bool
		}
		json.Unmarshal(e.Payload, &p)
		if p.Name == "read_plan_step" && p.OK && strings.Contains(p.Output, needle) {
			found = true
		}
	}
	if !found {
		t.Fatal("exact contract was unavailable through dispatched read_plan_step")
	}
	if len(m.bodies) < 2 || !strings.Contains(m.bodies[1], "Evidence receipt: evt_") {
		t.Fatal("model cannot cite the current tool observation")
	}
}

func TestSupervisorVerificationReviewBlocksPassingCheck(t *testing.T) {
	s := NewSupervisor(&Plan{Title: "review", Steps: []PlanStep{{Title: "current"}, {Title: "later"}}})
	v := Verdict{Pass: true, Check: "original criterion", Evidence: "observed pass", VerificationReview: &VerificationReview{Reason: "Criterion is too weak", ProposalID: "proposal"}}
	d := s.Record(0, v, -1)
	if !d.HandBack || d.Action != "blocked" || s.Steps[0].Status != "blocked" || !s.Steps[0].Verdict.Pass || s.Steps[1].Attempts != 0 {
		t.Fatalf("passing disputed check advanced: %+v %+v", d, s.Steps)
	}
	if summary := stepSummary(s.Steps[0]); !strings.Contains(summary, "Verification review requested") || !strings.Contains(summary, "check passed") {
		t.Fatal("report lost review or actual pass", summary)
	}
}

func TestStalePlanCannotRunOrSaveEvenWhenOldCheckWouldPass(t *testing.T) {
	m := newScriptedModel(textReply("must not run"))
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	current := &Plan{Title: "current", Steps: []PlanStep{{Title: "current", Verify: &plan.Verify{Kind: "contains", File: "index.html", Symbol: "not implemented"}}}}
	stale := &Plan{Title: "old", Steps: []PlanStep{{Title: "old", Verify: &plan.Verify{Kind: "contains", File: "index.html", Symbol: "435 lines"}}}}
	w, err := episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(episodic.Plan, current)
	w.Close()
	_, err = l.runPlanFrom(context.Background(), "s1", stale, NewSupervisor(stale), 0, true, "")
	var mismatch *planSnapshotError
	if !errors.As(err, &mismatch) || len(m.bodies) != 0 {
		t.Fatalf("stale plan executed: calls=%d err=%v", len(m.bodies), err)
	}
	w, err = episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	err = l.saveSupervisor(w, "s1", NewSupervisor(stale))
	w.Close()
	if !errors.As(err, &mismatch) {
		t.Fatalf("stale supervisor was saved: %v", err)
	}
	events, _ := episodic.Replay(journal)
	for _, ev := range events {
		if ev.Type == episodic.PlanState || ev.Type == episodic.ToolCall {
			t.Fatalf("stale plan wrote execution evidence: %s", ev.Type)
		}
	}
}
