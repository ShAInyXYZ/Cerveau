package loop

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
)

func legacyDeliveryFixture(t *testing.T) (string, []episodic.Event, *Supervisor, string) {
	t.Helper()
	path := t.TempDir() + "/events.jsonl"
	w, err := episodic.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Append(episodic.MsgUser, map[string]string{"text": "Build the world.\nDELIVERY ORDER\n1. Playable in the browser\n2. Persistent lighting"})
	p := &Plan{Title: "old subsystem order", AutonomyBudget: "high", Steps: []PlanStep{{Title: "lighting", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "light"}}, {Title: "renderer", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "render"}}}}
	root, err := w.Append(episodic.Plan, p)
	if err != nil {
		t.Fatal(err)
	}
	wr := w.Scoped(map[string]any{"session_id": "legacy-session", "run_id": "old-run", "schema_version": 2})
	wr.Append(episodic.RunState, map[string]any{"id": "old-run", "status": "running", "step": 0})
	verdicts := []Verdict{}
	for i, step := range p.Steps {
		wr.Append(episodic.Note, map[string]any{"kind": "verify_started", "index": i, "verify": step.Verify})
		v := Verdict{Pass: i == 0, Check: step.Verify.Describe(), Evidence: "Historical harness observation; repeated renderer operation failed."}
		ev, _ := wr.Append(episodic.Note, map[string]any{"kind": "verify_finished", "index": i, "verdict": v})
		v.EvidenceEventID = ev.ID
		verdicts = append(verdicts, v)
	}
	s := NewSupervisor(p)
	s.Steps[0].Status, s.Steps[0].Attempts, s.Steps[0].Rev, s.Steps[0].Verdict = "needs_reverify", 1, 2, &verdicts[0]
	// Match an already attempted Recover admission: pending, zero attempts,
	// but the last failed verdict remains the recovery target and evidence.
	s.Steps[1].Status, s.Steps[1].Attempts, s.Steps[1].Rev, s.Steps[1].Verdict = "pending", 0, 1, &verdicts[1]
	s.pairFails[[2]int{0, 1}] = 1
	s.reverify = []int{0}
	wr.Append(episodic.PlanState, supervisorState(s, root.ID))
	events, err := w.Events()
	if err != nil {
		t.Fatal(err)
	}
	return path, events, s, root.ID
}

func TestLegacyDeliveryResumePreservesIdentityHistoryAndRecoveryTarget(t *testing.T) {
	path, events, _, planID := legacyDeliveryFixture(t)
	s, id, err := ReducePlan(events)
	if err != nil {
		t.Fatal(err)
	}
	beforePlan, _ := json.Marshal(s.Plan)
	beforeState, _ := json.Marshal(supervisorState(s, id))
	beforeJournal, _ := os.ReadFile(path)
	if recoveryTarget(s) != 1 {
		t.Fatalf("fixture is not pending failed target: %+v", s.Steps)
	}
	boundary, err := planDeliveryAdmission(path, s.Plan, "legacy-session")
	if err != nil || !strings.Contains(boundary, "NOT validated") || !strings.Contains(boundary, planID) || !strings.Contains(boundary, "Original user requirements and final acceptance remain binding") {
		t.Fatalf("legacy admission: %q %v", boundary, err)
	}
	afterJournal, _ := os.ReadFile(path)
	afterPlan, _ := json.Marshal(s.Plan)
	afterState, _ := json.Marshal(supervisorState(s, id))
	if !bytes.Equal(beforeJournal, afterJournal) || !bytes.Equal(beforePlan, afterPlan) || !bytes.Equal(beforeState, afterState) || recoveryTarget(s) != 1 {
		t.Fatal("admission reset/mutated journal, contract, evidence, state, counters or recovery target")
	}
	prompt := effectiveStepPrompt(StepPrompt{Index: 1, Step: s.Plan.Steps[1], Verify: s.Plan.Steps[1].Verify}, s.Plan, 1, planID, t.TempDir())
	if !strings.Contains(prompt, "saved order is retained, not validated") || !strings.Contains(prompt, "No delivery-order waiver") {
		t.Fatal("legacy prompt claimed validation or omitted boundary")
	}
}

func TestLegacyDeliveryRejectsModernMarkersAndUnprovenHistory(t *testing.T) {
	_, base, s, _ := legacyDeliveryFixture(t)
	mutatePayload := func(events []episodic.Event, index int, f func(map[string]any)) {
		var p map[string]any
		json.Unmarshal(events[index].Payload, &p)
		f(p)
		events[index].Payload, _ = json.Marshal(p)
	}
	cases := map[string]func([]episodic.Event) []episodic.Event{
		"null contract": func(e []episodic.Event) []episodic.Event {
			mutatePayload(e, 1, func(p map[string]any) { p["delivery_contract"] = nil })
			return e
		},
		"case folded null contract": func(e []episodic.Event) []episodic.Event {
			mutatePayload(e, 1, func(p map[string]any) { p["DELIVERY_CONTRACT"] = nil })
			return e
		},
		"empty step ID": func(e []episodic.Event) []episodic.Event {
			mutatePayload(e, 1, func(p map[string]any) { p["steps"].([]any)[0].(map[string]any)["id"] = "" })
			return e
		},
		"null milestone": func(e []episodic.Event) []episodic.Event {
			mutatePayload(e, 1, func(p map[string]any) { p["steps"].([]any)[0].(map[string]any)["milestone_id"] = nil })
			return e
		},
		"empty revision": func(e []episodic.Event) []episodic.Event {
			mutatePayload(e, 1, func(p map[string]any) { p["revision"] = "" })
			return e
		},
		"empty amendment ledger": func(e []episodic.Event) []episodic.Event {
			mutatePayload(e, 1, func(p map[string]any) { p["amendments"] = []any{} })
			return e
		},
		"missing all observations":          func(e []episodic.Event) []episodic.Event { return e[:3] },
		"bare state is not execution proof": func(e []episodic.Event) []episodic.Event { return append(e[:3:3], e[len(e)-1]) },
		"unpaired finishes": func(e []episodic.Event) []episodic.Event {
			for i := range e {
				if i == 3 || i == 5 {
					e[i].Type = episodic.Checkpoint
				}
			}
			return e
		},
		"wrong committed check": func(e []episodic.Event) []episodic.Event {
			for _, i := range []int{3, 5} {
				mutatePayload(e, i, func(p map[string]any) {
					p["verify"] = map[string]string{"kind": "contains", "file": "world.js", "symbol": "weaker"}
				})
			}
			return e
		},
		"foreign plan observations": func(e []episodic.Event) []episodic.Event {
			for _, i := range []int{3, 4, 5, 6} {
				mutatePayload(e, i, func(p map[string]any) { p["plan_event_id"] = "evt_999999" })
			}
			return e
		},
		"foreign session pair cannot self-authorize": func(e []episodic.Event) []episodic.Event {
			for _, i := range []int{3, 4, 5, 6} {
				mutatePayload(e, i, func(p map[string]any) { p["session_id"] = "foreign-session" })
			}
			return e
		},
		"missing session envelope": func(e []episodic.Event) []episodic.Event {
			for _, i := range []int{3, 4, 5, 6} {
				mutatePayload(e, i, func(p map[string]any) { delete(p, "session_id") })
			}
			return e
		},
		"foreign raw plan owner": func(e []episodic.Event) []episodic.Event {
			mutatePayload(e, 1, func(p map[string]any) { p["session_id"] = "foreign-session" })
			return e
		},
		"prior plan proof": func(e []episodic.Event) []episodic.Event {
			replacement := e[1]
			replacement.ID = "evt_999999"
			return append(e, replacement)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			events := mutate(append([]episodic.Event(nil), base...))
			if _, _, ok := legacyDeliveryResumeEvidence(events, s.Plan, "legacy-session"); ok {
				t.Fatal("unsafe legacy bypass accepted")
			}
		})
	}
	copy := *s.Plan
	copy.Title = "stale caller"
	if _, _, ok := legacyDeliveryResumeEvidence(base, &copy, "legacy-session"); ok {
		t.Fatal("stale plan snapshot accepted")
	}
	for _, sid := range []string{"", "foreign-session"} {
		if _, _, ok := legacyDeliveryResumeEvidence(base, s.Plan, sid); ok {
			t.Fatalf("missing/wrong caller provenance accepted: %q", sid)
		}
	}
}

// Opt-in diagnostic only: never open a Writer, run a step/check/model, or read
// workspace sources. It proves admission against an actual pre-binding journal
// while asserting that neither its bytes nor its reduced contract/state changed.
func TestLegacyDeliveryActualJournalAdmissionReadOnly(t *testing.T) {
	path := os.Getenv("CRV_LEGACY_DELIVERY_JOURNAL")
	if path == "" {
		t.Skip("set CRV_LEGACY_DELIVERY_JOURNAL for read-only actual-journal admission")
	}
	sessionID := os.Getenv("CRV_LEGACY_DELIVERY_SESSION")
	if sessionID == "" {
		t.Fatal("CRV_LEGACY_DELIVERY_SESSION must explicitly identify the requested session")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	events, err := episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	s, id, err := ReducePlan(events)
	if err != nil {
		t.Fatal(err)
	}
	planBefore, _ := json.Marshal(s.Plan)
	stateBefore, _ := json.Marshal(supervisorState(s, id))
	target := recoveryTarget(s)
	boundary, err := planDeliveryAdmission(path, s.Plan, sessionID)
	if err != nil || boundary == "" {
		t.Fatalf("actual journal legacy admission rejected: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	planAfter, _ := json.Marshal(s.Plan)
	stateAfter, _ := json.Marshal(supervisorState(s, id))
	if !bytes.Equal(before, after) || !bytes.Equal(planBefore, planAfter) || !bytes.Equal(stateBefore, stateAfter) || target != recoveryTarget(s) {
		t.Fatal("read-only admission changed journal or effective state")
	}
	t.Logf("read-only admission accepted plan %s with %d unchanged steps; recovery target index %d; delivery order explicitly unvalidated", id, len(s.Plan.Steps), target)
}
