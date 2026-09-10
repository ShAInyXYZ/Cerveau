package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
)

func amendmentFixture(t *testing.T) (*episodic.Writer, string, string, *Supervisor, string) {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "fixture.txt"), []byte("original acceptance fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	w, err := episodic.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	w.Append(episodic.MsgUser, map[string]string{"text": "Preserve the original acceptance fixture and full checks."})
	p := &Plan{Title: "P", AutonomyBudget: "high", Steps: []PlanStep{{Title: "geometry", Detail: "Preserve invariant", Files: []string{"fixture.txt"}, Verify: &plan.Verify{Kind: "contains", File: "fixture.txt", Symbol: "expected invariant"}}, {ID: "independent", Title: "independent", Files: []string{"other.txt"}, Verify: &plan.Verify{Kind: "contains", File: "other.txt", Symbol: "ready"}}}}
	event, err := w.Append(episodic.Plan, p)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSupervisor(p)
	s.Steps[0].Status, s.Steps[0].Attempts, s.Steps[0].Rev = "running", 4, 1
	s.Steps[0].Verdict = &Verdict{Check: p.Steps[0].Verify.Describe(), Evidence: "earlier failure"}
	s.Steps[1].Status, s.Steps[1].Attempts = "passed", 2
	s.pairFails[[2]int{0, 1}] = 1
	w.Append(episodic.RunState, map[string]any{"id": "run", "run_id": "run", "session_id": "session", "status": "running", "step": 0})
	w.Append(episodic.PlanState, supervisorState(s, event.ID))
	w.Append(episodic.ToolCall, map[string]any{"run_id": "run", "session_id": "session", "id": "read", "name": "read", "args": map[string]string{"path": "fixture.txt"}})
	w.Append(episodic.ToolResult, map[string]any{"run_id": "run", "session_id": "session", "id": "read", "name": "read", "ok": true, "output": "Observed fixed geometry and existing lifecycle."})
	version := planAmendmentWorkspaceVersion(workspace, p.Steps[0])
	w.Append(episodic.Note, map[string]any{"run_id": "run", "session_id": "session", "kind": "verify_started", "index": 0, "verify": p.Steps[0].Verify})
	w.Append(episodic.Note, map[string]any{"run_id": "run", "session_id": "session", "kind": "verify_finished", "index": 0, "verdict": Verdict{Check: p.Steps[0].Verify.Describe(), Evidence: "invariant missing on repeated operation", WorkspaceVersion: "metadata-fingerprint", DeclaredSourceVersion: version}})
	return w, path, workspace, s, event.ID
}

func amendmentArgs(p *Plan, planID, version, guidance string) string {
	raw, _ := json.Marshal(planAmendmentInput{ExpectedRevision: effectivePlanRevision(planID, p), StepID: plan.StepID(p.Steps[0].ID, 0), ExpectedWorkspaceVersion: version, Guidance: guidance, Reason: "Observed repeated operation invalidates the assumed one-time initialization lifecycle; repair ownership within the same files and unchanged invariants.", EvidenceEventIDs: []string{"evt_000006", "evt_000008"}})
	return string(raw)
}

func recordAmendment(t *testing.T, w *episodic.Writer, a *PlanAmendment) episodic.Event {
	t.Helper()
	ev, err := w.Append(episodic.Note, map[string]any{"kind": "plan_guidance_amended", "session_id": a.SessionID, "run_id": a.RunID, "plan_event_id": a.PlanID, "amendment": a})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestPlanAmendmentReplayPreservesContractProgressAndBudgets(t *testing.T) {
	w, path, workspace, s, planID := amendmentFixture(t)
	events, _ := w.Events()
	before := immutablePlanSHA(s.Plan)
	version := planAmendmentWorkspaceVersion(workspace, s.Plan.Steps[0])
	args := amendmentArgs(s.Plan, planID, version, "Initialize and remove chunk lighting through one lifecycle owner.")
	a, err := newPlanAmendment(events, s.Plan, "session", "run", planID, 0, version, args)
	if err != nil {
		t.Fatal(err)
	}
	event := recordAmendment(t, w, a)
	// A stale state append may not overwrite the effective amendment or its
	// counters; a duplicate ledger entry may not consume another amendment.
	w.Append(episodic.PlanState, supervisorState(s, planID))
	recordAmendment(t, w, a)
	w.Append(episodic.RunState, map[string]any{"id": "run", "run_id": "run", "session_id": "session", "status": "interrupted", "step": 0})
	events, _ = w.Events()
	replayed, id, err := ReducePlan(events)
	if err != nil {
		t.Fatal(err)
	}
	if id != planID || replayed.Plan.Revision != event.ID || len(replayed.Plan.Amendments) != 1 || immutablePlanSHA(replayed.Plan) != before {
		t.Fatalf("bad replay: %+v", replayed.Plan)
	}
	if replayed.Steps[0].Attempts != 4 || replayed.Steps[0].Rev != 1 || replayed.Steps[0].Verdict.Evidence != "earlier failure" || replayed.Steps[1].Status != "passed" || replayed.Steps[1].Attempts != 2 || replayed.pairFails[[2]int{0, 1}] != 1 || replayed.Plan.AutonomyBudget != "high" {
		t.Fatalf("reset budget/history/unaffected progress: %+v", replayed)
	}
	if replayed.Steps[0].Status != "pending" {
		t.Fatalf("interruption was not replayed: %+v", replayed.Steps[0])
	}
	latest, latestID, err := LatestPlan(path)
	if err != nil || latestID != planID || !samePlanSnapshot(latest, replayed.Plan) {
		t.Fatalf("LatestPlan not effective: %+v %v", latest, err)
	}
	if _, err := requireCurrentPlan(path, s.Plan); err == nil {
		t.Fatal("stale snapshot accepted")
	}
	data, _ := os.ReadFile(filepath.Join(workspace, "fixture.txt"))
	if string(data) != "original acceptance fixture\n" {
		t.Fatal("fixture changed")
	}
	prompt := effectiveStepPrompt(StepPrompt{Step: latest.Steps[0], Verify: latest.Steps[0].Verify}, latest, 0, planID, workspace)
	if !strings.Contains(prompt, a.Input.Guidance) || !strings.Contains(prompt, "expected invariant") || !strings.Contains(prompt, event.ID) {
		t.Fatalf("effective prompt lost guidance, check or revision: %s", prompt)
	}
	reader, err := newPlanStepReader(planID, latest, replayed.Steps)
	if err != nil {
		t.Fatal(err)
	}
	read, err := reader.Execute(context.Background(), json.RawMessage(`{"index":0}`))
	if err != nil || !strings.Contains(read, a.Input.Guidance) || !strings.Contains(read, event.ID) {
		t.Fatalf("reader did not refresh: %s %v", read, err)
	}
}

func TestPlanAmendmentRejectsWeakeningStaleScopeAndEvidence(t *testing.T) {
	w, _, workspace, s, planID := amendmentFixture(t)
	events, _ := w.Events()
	version := planAmendmentWorkspaceVersion(workspace, s.Plan.Steps[0])
	valid := amendmentArgs(s.Plan, planID, version, "Use a single lifecycle owner.")
	cases := map[string]string{
		"replace predicate":  strings.TrimSuffix(valid, "}") + `,"proposed_verify":{"kind":"command","command":"exit 0"}}`,
		"weaken fixture":     strings.TrimSuffix(valid, "}") + `,"files":["weaker-test.js"]}`,
		"change authority":   strings.TrimSuffix(valid, "}") + `,"approved":true}`,
		"stale revision":     strings.Replace(valid, planID, "evt_999999", 1),
		"stale step":         strings.Replace(valid, `"step-1"`, `"independent"`, 1),
		"stale workspace":    strings.Replace(valid, version, "old-version", 1),
		"foreign evidence":   strings.Replace(valid, "evt_000008", "evt_999999", 1),
		"source only":        strings.Replace(valid, `,"evt_000008"`, "", 1),
		"duplicate evidence": strings.Replace(valid, "evt_000006", "evt_000008", 1),
		"null":               "null",
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := newPlanAmendment(events, s.Plan, "session", "run", planID, 0, version, args); err == nil {
				t.Fatalf("accepted unsafe input: %s", args)
			}
		})
	}
	if _, err := newPlanAmendment(events, s.Plan, "foreign", "run", planID, 0, version, valid); err == nil {
		t.Fatal("foreign session accepted")
	}
	events = append(events, verificationReviewTestEvent(t, 9, episodic.RunState, map[string]any{"id": "new-run", "session_id": "session", "status": "running", "step": 0}))
	if _, err := newPlanAmendment(events, s.Plan, "session", "run", planID, 0, version, valid); err == nil {
		t.Fatal("superseded owner accepted")
	}
}

func TestPlanAmendmentReplayRejectsForgedContractAndAuthority(t *testing.T) {
	w, _, workspace, s, planID := amendmentFixture(t)
	events, _ := w.Events()
	version := planAmendmentWorkspaceVersion(workspace, s.Plan.Steps[0])
	a, err := newPlanAmendment(events, s.Plan, "session", "run", planID, 0, version, amendmentArgs(s.Plan, planID, version, "Use existing geometry and preserve assertions."))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*PlanAmendment){func(a *PlanAmendment) { a.Authority = "approved_check_replacement" }, func(a *PlanAmendment) { a.OriginalStep.Verify.Symbol = "weaker" }, func(a *PlanAmendment) { a.Evidence[0].PayloadSHA256 = "forged" }, func(a *PlanAmendment) { a.ContractSHA256 = "forged" }} {
		var forged PlanAmendment
		raw, _ := json.Marshal(a)
		json.Unmarshal(raw, &forged)
		mutate(&forged)
		ev := verificationReviewTestEvent(t, 9, episodic.Note, map[string]any{"kind": "plan_guidance_amended", "session_id": "session", "run_id": "run", "amendment": forged})
		replayed, _, err := ReducePlan(append(append([]episodic.Event(nil), events...), ev))
		if err != nil || replayed.Plan.Revision != "" || len(replayed.Plan.Amendments) != 0 {
			t.Fatalf("forgery projected: %+v %v", replayed, err)
		}
	}
}

func TestPlanAmendmentBoundedEquivalentAndConcurrentAdmission(t *testing.T) {
	w, _, workspace, s, planID := amendmentFixture(t)
	version := planAmendmentWorkspaceVersion(workspace, s.Plan.Steps[0])
	args := amendmentArgs(s.Plan, planID, version, "Single lifecycle owner.")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dispatcher := &planAdaptationDispatcher{writer: w, sessionID: "session", runID: "run", planID: planID, index: 0, workspace: workspace, current: s.Plan}
			_, err := dispatcher.Execute(context.Background(), json.RawMessage(args))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent stale amendments accepted: %d", successes)
	}
	events, _ := w.Events()
	current, _, err := ReducePlan(events)
	if err != nil || len(current.Plan.Amendments) != 1 {
		t.Fatalf("ledger: %+v %v", current, err)
	}
	if _, err := newPlanAmendment(events, current.Plan, "session", "run", planID, 0, version, amendmentArgs(current.Plan, planID, version, " SINGLE   lifecycle owner. ")); err == nil {
		t.Fatal("equivalent retry accepted")
	}
	a, err := newPlanAmendment(events, current.Plan, "session", "run", planID, 0, version, amendmentArgs(current.Plan, planID, version, "Retain per-chunk identity until the unload operation completes."))
	if err != nil {
		t.Fatal(err)
	}
	recordAmendment(t, w, a)
	events, _ = w.Events()
	current, _, _ = ReducePlan(events)
	if _, err := newPlanAmendment(events, current.Plan, "session", "run", planID, 0, version, amendmentArgs(current.Plan, planID, version, "A third distinct strategy.")); err == nil {
		t.Fatal("step amendment ledger reset")
	}
}

func TestDeliveryRuntimeAdmissionAuthenticAndLegacy(t *testing.T) {
	w, path, _, s, _ := amendmentFixture(t)
	if err := validatePlanDelivery(path, s.Plan, "session"); err != nil {
		t.Fatalf("legacy prose rejected: %v", err)
	}
	request := "Build.\nDelivery order:\n1. Playable browser slice\n2. Persistence"
	user, _ := w.Append(episodic.MsgUser, map[string]string{"text": request})
	contract, err := plan.ParseDelivery(request, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := *s.Plan
	w.Append(episodic.Plan, &p)
	if err := validatePlanDelivery(path, &p, "session"); err == nil {
		t.Fatal("unbound legacy explicit delivery accepted")
	}
	p.Delivery = contract
	p.Steps = append([]PlanStep(nil), p.Steps...)
	p.Steps[0].MilestoneID = "milestone-1"
	p.Steps[1].MilestoneID = "milestone-2"
	if err := validatePlanDelivery(path, &p, "session"); err != nil {
		t.Fatal(err)
	}
	p.Steps[0].MilestoneID, p.Steps[1].MilestoneID = "milestone-2", "milestone-1"
	if err := validatePlanDelivery(path, &p, "session"); err == nil {
		t.Fatal("runtime order bypass accepted")
	}
	p.Steps[0].MilestoneID, p.Steps[1].MilestoneID = "milestone-1", "milestone-2"
	p.Delivery.Milestones[0].Text = "weaker milestone"
	if err := validatePlanDelivery(path, &p, "session"); err == nil {
		t.Fatal("forged user contract accepted")
	}
}

func TestPlanAmendmentRejectsByteChangeWithPreservedMetadata(t *testing.T) {
	w, _, workspace, s, planID := amendmentFixture(t)
	source := filepath.Join(workspace, "fixture.txt")
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(source)
	if err := os.WriteFile(source, []byte(strings.Repeat("x", len(old))), info.Mode()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	current := planAmendmentWorkspaceVersion(workspace, s.Plan.Steps[0])
	dispatcher := &planAdaptationDispatcher{writer: w, sessionID: "session", runID: "run", planID: planID, index: 0, workspace: workspace, current: s.Plan}
	if _, err := dispatcher.Execute(context.Background(), json.RawMessage(amendmentArgs(s.Plan, planID, current, "Use the old observation anyway."))); err == nil {
		t.Fatal("metadata-identical byte changes reused stale check evidence")
	}
}
