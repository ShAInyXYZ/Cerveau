package loop

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
)

type replayReviewEnvelope struct {
	Kind      string              `json:"kind"`
	Index     int                 `json:"index"`
	PlanID    string              `json:"plan_event_id"`
	SessionID string              `json:"session_id"`
	RunID     string              `json:"run_id"`
	Review    *VerificationReview `json:"review"`
	Verdict   *Verdict            `json:"verdict"`
}

// The fixture stops precisely after the durable review note, before the normal
// blocked PlanState write. The contains check records its receipt ID after its
// verify_finished payload is written, matching verifyStep's real ordering.
func verificationReviewReplayFixture(t *testing.T, pass bool, check ...*plan.Verify) ([]episodic.Event, *Plan) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	w, err := episodic.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	w = w.Scoped(map[string]any{"session_id": "session", "run_id": "run"})
	p := &Plan{Title: "review", Steps: []PlanStep{
		{Title: "mesh", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "face coverage"}},
		{Title: "next", Files: []string{"index.html"}, Verify: &plan.Verify{Kind: "contains", File: "index.html", Symbol: "renderer"}},
	}}
	if len(check) > 0 {
		p.Steps[0].Verify = check[0]
	}
	pe, _ := w.Append(episodic.Plan, p)
	w.Append(episodic.RunState, RunState{ID: "run", Kind: "autopilot", Status: "running", Phase: "verifying", Step: 0})
	s := NewSupervisor(p)
	s.Steps[0].Status = "running"
	w.Append(episodic.PlanState, supervisorState(s, pe.ID))
	w.Append(episodic.ToolCall, map[string]any{"id": "read", "name": "read", "args": map[string]string{"path": "world.js"}})
	evidence, _ := w.Append(episodic.ToolResult, map[string]any{"id": "read", "name": "read", "ok": true, "output": "observed flat fixture has 256 top faces"})
	w.Append(episodic.Note, map[string]any{"kind": "verify_started", "index": 0, "verify": p.Steps[0].Verify})
	current := Verdict{Pass: pass, Check: p.Steps[0].Verify.Describe(), Evidence: "fresh check observation", WorkspaceVersion: "current-sha"}
	if p.Steps[0].Verify.Kind == "command" {
		w.Append(episodic.ToolCall, map[string]any{"id": "verify", "name": "bash", "args": map[string]string{"command": p.Steps[0].Verify.Command}})
		result, _ := w.Append(episodic.ToolResult, map[string]any{"id": "verify", "name": "bash", "ok": pass, "output": current.Evidence})
		current.EvidenceEventID = result.ID
	}
	finished, _ := w.Append(episodic.Note, map[string]any{"kind": "verify_finished", "index": 0, "verdict": current})
	if current.EvidenceEventID == "" {
		current.EvidenceEventID = finished.ID
	}
	events, _ := episodic.Replay(path)
	request, err := newVerificationReview(events, "session", "run", pe.ID, 0, p.Steps[0].Verify, verificationReviewArgs(t, []string{evidence.ID}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := persistVerificationReview(w, request, current); err != nil {
		t.Fatal(err)
	}
	events, err = episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	return events, p
}

func TestVerificationReviewReplayBlocksAfterCrashBeforePlanState(t *testing.T) {
	for _, pass := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed-check", true: "passed-check"}[pass], func(t *testing.T) {
			events, _ := verificationReviewReplayFixture(t, pass)
			noteID := events[len(events)-1].ID
			events = append(events, verificationReviewTestEvent(t, 99, episodic.RunState, map[string]any{"id": "run", "run_id": "run", "session_id": "session", "status": "interrupted", "step": 0}))
			s, id, err := ReducePlan(events)
			if err != nil {
				t.Fatal(err)
			}
			st := s.Steps[0]
			if id != events[0].ID || st.Status != "blocked" || st.Attempts != 1 || st.Verdict == nil || st.Verdict.Pass != pass || st.Verdict.VerificationReview == nil {
				t.Fatalf("durable review disappeared after interruption: %+v", st)
			}
			if st.Verdict.VerificationReview.EventID != noteID || !strings.Contains(st.Reason, "Verification review requested") || s.Next() != -1 || s.Steps[1].Attempts != 0 {
				t.Fatalf("review identity/reason/advancement incorrect: %+v", s)
			}
		})
	}
}

func TestVerificationReviewReplayRejectsMismatchedScopeOrContract(t *testing.T) {
	tests := map[string]func(*replayReviewEnvelope){
		"foreign envelope plan":       func(n *replayReviewEnvelope) { n.PlanID = "evt_999999" },
		"foreign nested plan":         func(n *replayReviewEnvelope) { n.Review.PlanID = "evt_999999" },
		"different envelope index":    func(n *replayReviewEnvelope) { n.Index = 1 },
		"different nested index":      func(n *replayReviewEnvelope) { n.Review.Index = 1 },
		"out of bounds":               func(n *replayReviewEnvelope) { n.Index, n.Review.Index = 22, 22 },
		"foreign envelope session":    func(n *replayReviewEnvelope) { n.SessionID = "foreign" },
		"foreign nested session":      func(n *replayReviewEnvelope) { n.Review.SessionID = "foreign" },
		"foreign coherent session":    func(n *replayReviewEnvelope) { n.SessionID, n.Review.SessionID = "foreign", "foreign" },
		"foreign coherent run":        func(n *replayReviewEnvelope) { n.RunID, n.Review.RunID = "foreign", "foreign" },
		"foreign nested run":          func(n *replayReviewEnvelope) { n.Review.RunID = "foreign" },
		"different original":          func(n *replayReviewEnvelope) { n.Review.OriginalVerify.Symbol = "altered" },
		"different original hash":     func(n *replayReviewEnvelope) { n.Review.OriginalCheckSHA256 = "altered" },
		"different current check":     func(n *replayReviewEnvelope) { n.Verdict.Check = "altered" },
		"different current evidence":  func(n *replayReviewEnvelope) { n.Verdict.Evidence = "fabricated" },
		"different current pass":      func(n *replayReviewEnvelope) { n.Verdict.Pass = true },
		"different current workspace": func(n *replayReviewEnvelope) { n.Review.CurrentWorkspaceVersion = "altered" },
		"different receipt": func(n *replayReviewEnvelope) {
			n.Verdict.EvidenceEventID, n.Review.CurrentEvidenceEventID = "evt_000005", "evt_000005"
		},
		"approved proposal":     func(n *replayReviewEnvelope) { n.Review.Status = "approved" },
		"bad proposal identity": func(n *replayReviewEnvelope) { n.Review.ProposalID = "altered" },
		"wrong event identity":  func(n *replayReviewEnvelope) { n.Review.EventID = "evt_999999" },
		"missing verdict":       func(n *replayReviewEnvelope) { n.Verdict = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			events, _ := verificationReviewReplayFixture(t, false)
			last := &events[len(events)-1]
			var n replayReviewEnvelope
			if err := json.Unmarshal(last.Payload, &n); err != nil {
				t.Fatal(err)
			}
			mutate(&n)
			last.Payload, _ = json.Marshal(n)
			s, _, err := ReducePlan(events)
			if err != nil || s.Steps[0].Status != "running" || s.Steps[0].Verdict != nil {
				t.Fatalf("mismatched review projected: %+v %v", s, err)
			}
		})
	}
}

func TestVerificationReviewReplayRequiresCurrentRunAndFreshCheck(t *testing.T) {
	for _, remove := range []int{5, 6} { // verify_started, verify_finished
		t.Run(map[int]string{5: "missing-start", 6: "missing-finish"}[remove], func(t *testing.T) {
			events, _ := verificationReviewReplayFixture(t, false)
			events = append(events[:remove], events[remove+1:]...)
			s, _, err := ReducePlan(events)
			if err != nil || s.Steps[0].Status != "running" {
				t.Fatalf("unbound review projected: %+v %v", s, err)
			}
		})
	}
	for _, change := range []string{"other run", "different exact check", "newer check"} {
		t.Run(change, func(t *testing.T) {
			events, _ := verificationReviewReplayFixture(t, false)
			var payload map[string]any
			json.Unmarshal(events[5].Payload, &payload)
			switch change {
			case "other run":
				payload["run_id"] = "foreign"
			case "different exact check":
				payload["verify"] = &plan.Verify{Kind: "contains", File: "world.js", Symbol: "a different contract"}
			case "newer check":
				json.Unmarshal(events[6].Payload, &payload)
				payload["verdict"] = Verdict{Check: "newer result", Evidence: "newer check"}
				extra := verificationReviewTestEvent(t, 90, episodic.Note, payload)
				events = append(events[:7], extra, events[7])
			}
			if change != "newer check" {
				events[5].Payload, _ = json.Marshal(payload)
			}
			s, _, err := ReducePlan(events)
			if err != nil || s.Steps[0].Status != "running" {
				t.Fatalf("stale/unbound check projected: %+v %v", s, err)
			}
		})
	}
}

func TestVerificationReviewReplayAllowsLegacyMissingPlanScope(t *testing.T) {
	for _, missingOwner := range []bool{false, true} {
		events, _ := verificationReviewReplayFixture(t, false)
		var p Plan
		json.Unmarshal(events[0].Payload, &p)
		events[0].Payload, _ = json.Marshal(p) // legacy plan has no session envelope
		if missingOwner {
			events = append(events[:1], events[2:]...)
		}
		s, _, err := ReducePlan(events)
		if err != nil || s.Steps[0].Status != "blocked" {
			t.Fatalf("valid matching note/check scope rejected only for missing legacy envelope: %+v %v", s, err)
		}
	}
}

func TestVerificationReviewReplayNewerStateAndPlanSupersedeNote(t *testing.T) {
	events, p := verificationReviewReplayFixture(t, false)
	s := NewSupervisor(p)
	s.Steps[0].Status = "passed"
	s.Steps[0].Verdict = &Verdict{Pass: true, Check: p.Steps[0].Verify.Describe(), Evidence: "later unchanged check passed"}
	events = append(events, verificationReviewTestEvent(t, 90, episodic.PlanState, supervisorState(s, events[0].ID)))
	got, _, err := ReducePlan(events)
	if err != nil || got.Steps[0].Status != "passed" || got.Steps[0].Verdict.VerificationReview != nil {
		t.Fatalf("old review overrode newer authoritative PlanState: %+v %v", got, err)
	}
	events = append(events, verificationReviewTestEvent(t, 91, episodic.Plan, p))
	got, id, err := ReducePlan(events)
	if err != nil || id != "evt_000091" || got.Steps[0].Status != "pending" {
		t.Fatalf("old review leaked into replacement plan: %+v %s %v", got, id, err)
	}
}

func TestVerificationReviewReplayPreservesCommandResultAndDeduplicatesNote(t *testing.T) {
	events, _ := verificationReviewReplayFixture(t, false, &plan.Verify{Kind: "command", Command: "node tests.mjs"})
	note := events[len(events)-1]
	var saved replayReviewEnvelope
	json.Unmarshal(note.Payload, &saved)
	note.ID = "evt_000099"
	events = append(events, note)
	s, _, err := ReducePlan(events)
	if err != nil || s.Steps[0].Status != "blocked" || s.Steps[0].Attempts != 1 || s.Steps[0].Verdict.EvidenceEventID != saved.Verdict.EvidenceEventID {
		t.Fatalf("command receipt lost or duplicate counted as an attempt: %+v %v", s, err)
	}
}
