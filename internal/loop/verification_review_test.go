package loop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
)

func verificationReviewFixture(t *testing.T) ([]episodic.Event, *plan.Verify) {
	t.Helper()
	v := &plan.Verify{Kind: "contains", File: "fixture.txt", Symbol: "expected contract"}
	return []episodic.Event{
		verificationReviewTestEvent(t, 1, episodic.Plan, map[string]any{"steps": []PlanStep{{Title: "old", Verify: v}}}),
		verificationReviewTestEvent(t, 2, episodic.ToolCall, map[string]any{"run_id": "old-run", "session_id": "session", "id": "probe", "name": "read", "args": map[string]string{"path": "fixture.txt"}}),
		verificationReviewTestEvent(t, 3, episodic.ToolResult, map[string]any{"run_id": "old-run", "session_id": "session", "id": "probe", "name": "read", "ok": true, "output": "old plan observation"}),
		verificationReviewTestEvent(t, 4, episodic.Plan, map[string]any{"session_id": "session", "title": "fixture", "steps": []PlanStep{{Title: "geometry", Files: []string{"fixture.txt"}, Verify: v}}}),
		verificationReviewTestEvent(t, 5, episodic.ToolCall, map[string]any{"run_id": "run", "session_id": "session", "id": "probe", "name": "read", "args": map[string]string{"path": "fixture.txt"}}),
		verificationReviewTestEvent(t, 6, episodic.ToolResult, map[string]any{"run_id": "run", "session_id": "session", "id": "probe", "name": "read", "ok": true, "output": "The source fixture is a flat 16 by 16 top surface."}),
		verificationReviewTestEvent(t, 7, episodic.Note, map[string]any{"run_id": "run", "session_id": "session", "kind": "verify_started", "index": 0, "verify": v}),
		verificationReviewTestEvent(t, 8, episodic.Note, map[string]any{"run_id": "run", "session_id": "session", "kind": "verify_finished", "index": 0,
			"verdict": Verdict{Check: v.Describe(), Evidence: "The committed check fails.", WorkspaceVersion: "observed-version"}}),
	}, v
}

func verificationReviewTestEvent(t *testing.T, n int, kind episodic.EventType, payload any) episodic.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return episodic.Event{ID: fmt.Sprintf("evt_%06d", n), Type: kind, Payload: raw}
}

func verificationReviewArgs(t *testing.T, refs []string, proposed *plan.Verify) string {
	t.Helper()
	args := map[string]any{"reason": "The observed fixture cannot satisfy the committed numeric condition; review the fixture assumptions.", "evidence_event_ids": refs}
	if proposed != nil {
		args["proposed_verify"] = proposed
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestVerificationReviewPreservesContractAndDoesNotExecuteProposal(t *testing.T) {
	events, original := verificationReviewFixture(t)
	workspace := t.TempDir()
	benchmark, marker := filepath.Join(workspace, "original-benchmark.txt"), filepath.Join(workspace, "proposal-ran")
	before := []byte("original acceptance assertions\n")
	if err := os.WriteFile(benchmark, before, 0600); err != nil {
		t.Fatal(err)
	}
	proposal := &plan.Verify{Kind: "command", Command: "touch " + marker + "; printf altered > " + benchmark}
	originalRaw, _ := json.Marshal(original)
	request, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original,
		verificationReviewArgs(t, []string{"evt_000006", "evt_000008"}, proposal))
	if err != nil {
		t.Fatal(err)
	}
	var stop *verificationReviewRequest
	if !errors.As(request, &stop) || !strings.Contains(stop.Error(), "Human review required") {
		t.Fatalf("request is not an explicit review stop: %v", request)
	}
	if request.Review.OriginalVerify == original || request.Review.OriginalCheckSHA256 != recoverySHA(originalRaw) {
		t.Fatal("review failed to copy and bind the committed criterion")
	}
	if request.Review.Status != "human_review_required" || request.Review.ProposedVerifyStatus != "unvalidated_not_authorized" {
		t.Fatalf("proposal claims authorization or validation: %+v", request.Review)
	}
	journal := filepath.Join(t.TempDir(), "events.jsonl")
	w, err := episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	current := Verdict{Check: original.Describe(), Evidence: "original still fails", EvidenceEventID: "evt_000008", WorkspaceVersion: "current-version"}
	if err := persistVerificationReview(w, request, current); err != nil {
		t.Fatal(err)
	}
	if request.Review.CurrentCheckPass || request.Review.CurrentEvidenceEventID != current.EvidenceEventID || request.Review.CurrentWorkspaceVersion != current.WorkspaceVersion {
		t.Fatalf("review lost the actual unchanged check result: %+v", request.Review)
	}
	if request.Review.ProposalID == "" || request.Review.EventID == "" {
		t.Fatal("review lacks durable proposal/event identity")
	}
	replayed, err := episodic.Replay(journal)
	if err != nil || len(replayed) != 1 || replayed[0].Type != episodic.Note {
		t.Fatalf("proposal unexpectedly wrote plan/tool events: %+v %v", replayed, err)
	}
	var saved struct {
		Kind      string
		Review    VerificationReview
		Verdict   Verdict
		SessionID string `json:"session_id"`
		RunID     string `json:"run_id"`
	}
	if err := json.Unmarshal(replayed[0].Payload, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Kind != "verification_review_requested" || saved.Review.ProposalID != request.Review.ProposalID || saved.Review.OriginalCheckSHA256 != recoverySHA(originalRaw) {
		t.Fatalf("durable review lost original contract or identity: %+v", saved)
	}
	if saved.SessionID != "session" || saved.RunID != "run" || saved.Verdict.Check != current.Check || saved.Verdict.EvidenceEventID != current.EvidenceEventID || saved.Verdict.VerificationReview != nil {
		t.Fatalf("durable review lost its scoped, fresh verdict: %+v", saved)
	}
	if raw, _ := json.Marshal(original); string(raw) != string(originalRaw) {
		t.Fatal("original criterion was mutated")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("proposed command ran: %v", err)
	}
	if after, err := os.ReadFile(benchmark); err != nil || string(after) != string(before) {
		t.Fatalf("acceptance benchmark was changed: %q %v", after, err)
	}
	// Review copies must remain isolated even if another owner later changes
	// its in-memory Plan object; that never edits the recorded original.
	original.Symbol = "changed in caller"
	if request.Review.OriginalVerify.Symbol != "expected contract" {
		t.Fatal("caller mutation changed review's original verification")
	}
}

func TestVerificationReviewRejectsMalformedAndUnauthorizedInputs(t *testing.T) {
	events, original := verificationReviewFixture(t)
	valid := verificationReviewArgs(t, []string{"evt_000006"}, nil)
	tests := map[string]string{
		"approval flag":          strings.TrimSuffix(valid, "}") + `,"approved":true}`,
		"model original":         strings.TrimSuffix(valid, "}") + `,"original_verify":{"kind":"command","command":"exit 0"}}`,
		"model plan identity":    strings.TrimSuffix(valid, "}") + `,"plan_event_id":"evt_000004"}`,
		"nested approval flag":   strings.TrimSuffix(valid, "}") + `,"proposed_verify":{"kind":"contains","file":"x","symbol":"x","approved":true}}`,
		"trivial proposed check": strings.TrimSuffix(valid, "}") + `,"proposed_verify":{"kind":"command","command":"test -f x"}}`,
		"empty reason":           `{"reason":" ","evidence_event_ids":["evt_000006"]}`,
		"missing refs":           `{"reason":"The criterion conflicts with the fixture."}`,
		"empty refs":             `{"reason":"contradiction","evidence_event_ids":[]}`,
		"empty ref":              `{"reason":"contradiction","evidence_event_ids":[""]}`,
		"malformed ref":          `{"reason":"contradiction","evidence_event_ids":["evt_000006#args.content"]}`,
		"duplicate refs":         `{"reason":"contradiction","evidence_event_ids":["evt_000006","evt_000006"]}`,
		"missing event":          `{"reason":"contradiction","evidence_event_ids":["evt_999999"]}`,
		"trailing object":        valid + `{}`,
		"array":                  `[]`,
		"null":                   `null`,
		"oversized arguments":    strings.Repeat(" ", verificationReviewArgsLimit+1),
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			if request, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, args); err == nil || request != nil {
				t.Fatalf("invalid/model-authorized proposal was accepted: %+v %v", request, err)
			}
		})
	}
	oversizedReason := map[string]any{"reason": strings.Repeat("x", verificationReviewReasonMax+1), "evidence_event_ids": []string{"evt_000006"}}
	raw, _ := json.Marshal(oversizedReason)
	if _, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, string(raw)); err == nil {
		t.Fatal("unbounded reason accepted")
	}
	tooMany := make([]string, verificationReviewRefsMax+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("evt_%06d", i+1)
	}
	if _, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, verificationReviewArgs(t, tooMany, nil)); err == nil {
		t.Fatal("unbounded evidence list accepted")
	}
}

func TestVerificationReviewIsolatesObservedEvidence(t *testing.T) {
	type extraEvent struct {
		kind    episodic.EventType
		payload map[string]any
	}
	tests := []struct {
		name  string
		extra []extraEvent
	}{
		{name: "user prose", extra: []extraEvent{{episodic.MsgUser, map[string]any{"text": "the criterion is wrong"}}}},
		{name: "model prose", extra: []extraEvent{{episodic.MsgAssistant, map[string]any{"text": "I proved the criterion is wrong"}}}},
		{name: "unpaired tool result", extra: []extraEvent{{episodic.ToolResult, map[string]any{"run_id": "run", "id": "orphan", "name": "read", "ok": true, "output": "alleged observation"}}}},
		{name: "cross-run pairing", extra: []extraEvent{{episodic.ToolCall, map[string]any{"run_id": "run", "id": "pair", "name": "read"}},
			{episodic.ToolResult, map[string]any{"run_id": "foreign-run", "id": "pair", "name": "read", "ok": true, "output": "alleged observation"}}}},
		{name: "foreign session", extra: []extraEvent{{episodic.ToolCall, map[string]any{"run_id": "run", "session_id": "another-session", "id": "pair", "name": "read"}},
			{episodic.ToolResult, map[string]any{"run_id": "run", "session_id": "another-session", "id": "pair", "name": "read", "ok": true, "output": "foreign source"}}}},
		{name: "foreign plan", extra: []extraEvent{{episodic.ToolCall, map[string]any{"run_id": "run", "plan_event_id": "evt_000001", "id": "pair", "name": "read"}},
			{episodic.ToolResult, map[string]any{"run_id": "run", "plan_event_id": "evt_000001", "id": "pair", "name": "read", "ok": true, "output": "foreign source"}}}},
		{name: "model control acknowledgement", extra: []extraEvent{{episodic.ToolCall, map[string]any{"run_id": "run", "id": "pair", "name": "request_revision"}},
			{episodic.ToolResult, map[string]any{"run_id": "run", "id": "pair", "name": "request_revision", "ok": true, "output": "model reason repeated by tool"}}}},
		{name: "unpaired verification note", extra: []extraEvent{{episodic.Note, map[string]any{"run_id": "run", "kind": "verify_finished", "index": 0,
			"verdict": Verdict{Check: "not an actual check", Evidence: "alleged observation"}}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events, original := verificationReviewFixture(t)
			for i, extra := range tc.extra {
				events = append(events, verificationReviewTestEvent(t, 9+i, extra.kind, extra.payload))
			}
			ref := events[len(events)-1].ID
			if request, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, verificationReviewArgs(t, []string{ref}, nil)); err == nil || request != nil {
				t.Fatalf("non-observation accepted as evidence: %+v %v", request, err)
			}
		})
	}
	for _, ref := range []string{"evt_000003", "evt_000004", "evt_000005", "evt_000007"} {
		events, original := verificationReviewFixture(t)
		if _, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, verificationReviewArgs(t, []string{ref}, nil)); err == nil {
			t.Fatalf("old-plan or non-result evidence %s accepted", ref)
		}
	}
}

func TestVerificationReviewAllowsHistoricalSamePlanObservations(t *testing.T) {
	events, original := verificationReviewFixture(t)
	events = append(events,
		verificationReviewTestEvent(t, 9, episodic.ToolCall, map[string]any{"run_id": "previous-run", "session_id": "session", "id": "read", "name": "read"}),
		verificationReviewTestEvent(t, 10, episodic.ToolResult, map[string]any{"run_id": "previous-run", "session_id": "session", "id": "read", "name": "read", "ok": true, "output": "historical source"}))
	request, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, verificationReviewArgs(t, []string{"evt_000006", "evt_000010"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if request.Review.Evidence[0].Historical || !request.Review.Evidence[1].Historical || request.Review.Evidence[1].RunID != "previous-run" {
		t.Fatalf("historical evidence presented as current: %+v", request.Review.Evidence)
	}
	if request.Review.ProposedVerify != nil || request.Review.ProposedVerifyStatus != "" {
		t.Fatal("optional replacement was invented")
	}
}

func TestVerificationReviewRejectsOverlappingCallIDsButAllowsCompletedReuse(t *testing.T) {
	events, original := verificationReviewFixture(t)
	call := map[string]any{"run_id": "run", "session_id": "session", "id": "reused", "name": "read"}
	result := map[string]any{"run_id": "run", "session_id": "session", "id": "reused", "name": "read", "ok": true, "output": "observed source"}
	for i, kind := range []episodic.EventType{episodic.ToolCall, episodic.ToolCall, episodic.ToolResult, episodic.ToolResult, episodic.ToolCall, episodic.ToolResult, episodic.ToolCall, episodic.ToolResult} {
		payload := call
		if kind == episodic.ToolResult {
			payload = result
		}
		events = append(events, verificationReviewTestEvent(t, i+9, kind, payload))
	}
	for _, ref := range []string{"evt_000011", "evt_000012"} {
		if _, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, verificationReviewArgs(t, []string{ref}, nil)); err == nil {
			t.Fatalf("ambiguous duplicate call identity accepted: %s", ref)
		}
	}
	for _, ref := range []string{"evt_000014", "evt_000016"} {
		if _, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, verificationReviewArgs(t, []string{ref}, nil)); err != nil {
			t.Fatalf("sequential completed ID reuse rejected: %s %v", ref, err)
		}
	}
}

func TestVerificationReviewRejectsStalePlanAndChangedContract(t *testing.T) {
	events, original := verificationReviewFixture(t)
	args := verificationReviewArgs(t, []string{"evt_000006"}, nil)
	changed := *original
	changed.Symbol = "different contract"
	if _, err := newVerificationReview(events, "session", "run", "evt_000004", 0, &changed, args); err == nil {
		t.Fatal("mismatched original criterion accepted")
	}
	if _, err := newVerificationReview(events, "session", "run", "evt_000001", 0, original, args); err == nil {
		t.Fatal("superseded plan accepted")
	}
	if _, err := newVerificationReview(events, "session", "run", "evt_000004", 1, original, args); err == nil {
		t.Fatal("nonexistent step accepted")
	}
	events = append(events, verificationReviewTestEvent(t, 9, episodic.Plan, map[string]any{"title": "replacement", "steps": []PlanStep{{Title: "new", Verify: original}}}))
	if _, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, args); err == nil {
		t.Fatal("newly superseded plan accepted")
	}
}

func TestVerificationReviewPersistsActualPassWithoutAuthorizingProposal(t *testing.T) {
	events, original := verificationReviewFixture(t)
	request, err := newVerificationReview(events, "session", "run", "evt_000004", 0, original, verificationReviewArgs(t, []string{"evt_000008"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	w, err := episodic.Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err := persistVerificationReview(w, request, Verdict{Pass: true, Check: original.Describe(), Evidence: "now passes", EvidenceEventID: "evt_000008", WorkspaceVersion: "current-version"}); err != nil {
		t.Fatal(err)
	}
	if !request.Review.CurrentCheckPass || request.Review.Status != "human_review_required" {
		t.Fatal("review changed observed pass or treated it as approval")
	}
	if err := persistVerificationReview(w, request, Verdict{Check: original.Describe()}); err == nil {
		t.Fatal("duplicate persistence accepted")
	}
	other, _ := newVerificationReview(events, "session", "run", "evt_000004", 0, original, verificationReviewArgs(t, []string{"evt_000008"}, nil))
	if err := persistVerificationReview(w, other, Verdict{Pass: true, Check: "a different criterion"}); err == nil {
		t.Fatal("current result from a different criterion accepted")
	}
}
