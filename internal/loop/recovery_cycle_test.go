package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
	"cerveau/internal/tools"
)

func TestRecoveryCycleDoesNotCreditMutationOrRepeatedFailure(t *testing.T) {
	c := newRecoveryCycle("v0")
	c.checked("v0", Verdict{Evidence: "Error: block=0"}, true)
	if c.takeCredit() {
		t.Fatal("baseline is not repair progress")
	}
	c.repair("v1", 1)
	if c.takeCredit() {
		t.Fatal("unchecked edit earned budget")
	}
	c.checked("v1", Verdict{Evidence: "Error: block=0"}, false)
	if c.takeCredit() {
		t.Fatal("same failure after edit earned budget")
	}
	c.repair("v2", 2)
	c.checked("v2", Verdict{Evidence: "Error: block=15"}, false)
	if !c.takeCredit() || c.takeCredit() {
		t.Fatal("new checked evidence must earn at most one slice")
	}
	c.repair("v3", 3)
	c.checked("v3", Verdict{Evidence: "Error: block=0"}, false)
	if c.takeCredit() || !c.exhausted() {
		t.Fatal("oscillation bypassed cycle bound")
	}
}

func TestRecoveryCycleRecognizesNewFunctionWithoutCreditingLineChurn(t *testing.T) {
	c := newRecoveryCycle("baseline")
	check := func(fn string, line int) Verdict {
		return Verdict{Evidence: fmt.Sprintf("TypeError: Cannot read properties of undefined (reading 'sky')\n    at Object.%s (file:///workspace/world.js:%d:31)\n    at testSky (file:///workspace/tests.mjs:324:15)\n    at ModuleJob.run (node:internal/modules/esm/module_job:222:25)", fn, line)}
	}
	c.checked("baseline", check("flushUpdates", 332), true)
	c.checked("repair1", check("invariants", 447), false)
	if !c.takeCredit() {
		t.Fatal("new failing lifecycle function must get one bounded diagnostic opportunity")
	}
	c.checked("repair2", check("invariants", 510), false)
	if c.takeCredit() {
		t.Fatal("line-number churn is not a new failure")
	}
	c.checked("repair3", check("flushUpdates", 399), false)
	if c.takeCredit() || !c.exhausted() {
		t.Fatal("returning to the old failure must preserve the three-cycle stop")
	}
}

func TestRecoveryCycleChecksSmallRepairBatchAndDoesNotCountTouch(t *testing.T) {
	c := newRecoveryCycle("same bytes")
	c.repair("same bytes", 1)
	c.repair("same bytes", 2)
	if c.due("same bytes", 3, false) {
		t.Fatal("unchanged source needs no repeated check")
	}
	c.repair("changed", 3)
	if c.due("changed", 4, false) {
		t.Fatal("one hunk may need a companion")
	}
	c.repair("changed again", 4)
	if !c.due("changed again", 5, false) {
		t.Fatal("repair batch escaped check")
	}
	c.checked("changed again", Verdict{Evidence: "still wrong"}, false)
	c.repair("next", 5)
	if !c.due("next", 9, false) || !c.due("next", 6, true) {
		t.Fatal("deferred check can be starved")
	}
}

func TestRecoveryStepVersionIgnoresMtimeAndUnrelatedDiagnosticFiles(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "world.js")
	os.WriteFile(path, []byte("same source"), 0600)
	step := PlanStep{Files: []string{"world.js"}}
	first := recoveryStepVersion(ws, step)
	os.Chtimes(path, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	os.WriteFile(filepath.Join(ws, "diagnostic.txt"), []byte("new notes"), 0600)
	if recoveryStepVersion(ws, step) != first {
		t.Fatal("mtime/diagnostic output credited as source repair")
	}
	os.WriteFile(path, []byte("changed source"), 0600)
	if recoveryStepVersion(ws, step) == first {
		t.Fatal("source content change missed")
	}
}

func TestRecoveryCurrentCheckPrecedesGuardAndOldToolFailure(t *testing.T) {
	v := Verdict{Evidence: "Error: current block=0", EvidenceEventID: "evt_current"}
	withExecutionStop(&v, errors.New("iteration cap (32) reached; old block=15"))
	if !strings.HasPrefix(v.Evidence, "Error: current block=0") || strings.Contains(v.Evidence, "old block") {
		t.Fatalf("failure pollution: %+v", v)
	}
	if !strings.Contains(v.ExecutionStop, "iteration cap") || v.EvidenceEventID != "evt_current" {
		t.Fatal(v)
	}
}

func TestRecoveryCheckExcerptKeepsDiagnosticAfterLongCommand(t *testing.T) {
	out := "SYNTAX_OK\nfile:///a.js:1\n" + strings.Repeat("long source ", 180) + "\n" + strings.Repeat(" ", 1000) + "^\n\nError: {\"sky\":0,\"block\":0}\n    at x (a.js:1:1)\nNode.js v22\n"
	s := verificationEvidence(out)
	if !strings.Contains(s, `Error: {"sky":0,"block":0}`) || len(s) > 4000 {
		t.Fatal(s)
	}
}

func TestRecoveryCycleStopsChurnWithoutAutomaticRetry(t *testing.T) {
	var replies []map[string]any
	for i := 0; i < 12; i++ {
		replies = append(replies, toolCall("write", fmt.Sprintf(`{"path":"world.js","content":"wrong version %d"}`, i)))
	}
	m := newScriptedModel(replies...)
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	ws := t.TempDir()
	l.SetWorkspaceFunc(func(string) string { return ws })
	if err := os.WriteFile(filepath.Join(ws, "world.js"), []byte("wrong baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	l.SetRegistry(tools.NewRegistry(tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSafe}))
	w, _ := episodic.Open(journal)
	p := &Plan{Title: "churn", Steps: []PlanStep{{Title: "repair", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "correct"}}}}
	w.Append(episodic.Plan, p)
	w.Append(episodic.Checkpoint, map[string]any{"index": 0, "status": "blocked", "evidence": "previous check failed"})
	w.Close()
	result, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1, Continue: true})
	if err != nil || result.StopReason != "plan_blocked" {
		t.Fatalf("%+v %v", result, err)
	}
	if len(m.bodies) != 6 {
		t.Fatalf("expected three 2-repair/check cycles, got %d model rounds", len(m.bodies))
	}
	if !strings.Contains(m.bodies[2], "RECOVERY CHECKPOINT RESULT") || !strings.Contains(m.bodies[2], "does not contain") {
		t.Fatal("model did not receive authoritative repair feedback")
	}
	st, _ := l.PlanStateOf("s1")
	if st.Steps[0].Attempts != 1 || st.Steps[0].Status != "blocked" {
		t.Fatalf("fresh retry erased bounded stop: %+v", st.Steps)
	}
	if !strings.Contains(result.Reply, "3 repair/check cycles") {
		t.Fatal(result.Reply)
	}
}

func TestRecoveryAlreadyFixedSkipsModelAndContinues(t *testing.T) {
	m := newScriptedModel(textReply("following step"))
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	ws := t.TempDir()
	l.SetWorkspaceFunc(func(string) string { return ws })
	os.WriteFile(filepath.Join(ws, "world.js"), []byte("correct"), 0600)
	w, _ := episodic.Open(journal)
	w.Append(episodic.Plan, &Plan{Title: "recheck", Steps: []PlanStep{
		{Title: "repair", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "correct"}},
		{Title: "next", Files: []string{"other.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "correct"}},
	}})
	w.Append(episodic.Checkpoint, map[string]any{"index": 0, "status": "blocked", "evidence": "old error"})
	w.Close()
	result, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1, Continue: true})
	if err != nil || result.StopReason == "plan_blocked" || len(m.bodies) != 1 {
		t.Fatalf("unnecessary repair: %+v calls=%d err=%v", result, len(m.bodies), err)
	}
}

func TestRecoveryOutputExhaustionDoesNotAutomaticallyResetBudget(t *testing.T) {
	m := newScriptedModel(emptyLengthReply())
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	l.SetThinking("autopilot", "medium")
	ws := t.TempDir()
	l.SetWorkspaceFunc(func(string) string { return ws })
	os.WriteFile(filepath.Join(ws, "world.js"), []byte("wrong"), 0600)
	w, _ := episodic.Open(journal)
	w.Append(episodic.Plan, &Plan{Title: "output budget", Steps: []PlanStep{{Title: "repair", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "correct"}}}})
	w.Append(episodic.Checkpoint, map[string]any{"index": 0, "status": "blocked", "evidence": "old failure"})
	w.Close()
	result, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1, Continue: true})
	if err != nil || result.StopReason != "plan_blocked" || len(m.bodies) != 2 {
		t.Fatalf("exhausted recovery automatically restarted: calls=%d result=%+v err=%v", len(m.bodies), result, err)
	}
	st, _ := l.PlanStateOf("s1")
	if !strings.Contains(st.Steps[0].Verdict.ExecutionStop, "output limit") || strings.Contains(st.Steps[0].Verdict.Evidence, "output limit") {
		t.Fatal(st.Steps[0].Verdict)
	}
}
