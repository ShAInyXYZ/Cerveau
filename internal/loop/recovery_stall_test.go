package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
)

func emptyLengthReply() map[string]any {
	return map[string]any{"finish_reason": "length", "message": map[string]any{"role": "assistant", "content": ""}}
}

func TestAssistantJournalRetainsFinishReason(t *testing.T) {
	got := assistantPayload(llm.Message{FinishReason: "length"}, llm.Usage{})
	if got["finish_reason"] != "length" {
		t.Fatal("output stop reason lost from journal")
	}
}

func TestRecoverySearchPaginationIsBounded(t *testing.T) {
	r := &recoveryReader{events: map[string][]byte{}}
	for i := 0; i < 25; i++ {
		r.events[fmt.Sprintf("evt_%06d", i)] = []byte(`{"name":"write","args":{"content":"rawBlock"}}`)
	}
	// Do not recursively surface results of a previous recovery search.
	r.events["evt_000026"] = []byte(`{"name":"recovery_read","output":"rawBlock"}`)
	out, err := r.Execute(context.Background(), json.RawMessage(`{"query":"rawBlock"}`))
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Matches   []any
		NextAfter string `json:"next_after"`
		EOF       bool
	}
	if err = json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Matches) != 20 || page.EOF || page.NextAfter != "evt_000005#args.content" {
		t.Fatalf("first page %s", out)
	}
	args, _ := json.Marshal(map[string]string{"query": "rawBlock", "after": page.NextAfter})
	out, err = r.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Matches) != 5 || !page.EOF {
		t.Fatalf("second page %s", out)
	}
}

// Run one real step attempt, not the outer supervisor's automatic retry.
func runRecoveryAttempt(t *testing.T, m *scriptedModel, thinking string) (string, error, []episodic.Event) {
	t.Helper()
	l, path := gateFixture(t, m)
	l.SetThinking("autopilot", thinking)
	// Keep the journal outside the artifact tree, as in production; journal
	// appends must not be mistaken for source progress by the fingerprint.
	ws := t.TempDir()
	l.SetWorkspaceFunc(func(string) string { return ws })
	if err := os.WriteFile(filepath.Join(ws, "world.js"), []byte("one\ntwo\nthree\nfour\nfive\nsix\n"), 0600); err != nil {
		t.Fatal(err)
	}
	l.SetRegistry(tools.NewRegistry(tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe}, tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSensitive}))
	p := &Plan{Title: "repair", Steps: []PlanStep{{Title: "source", Files: []string{"world.js"}}}}
	s := NewSupervisor(p)
	s.Steps[0].Reason = "SyntaxError: Unexpected end of input"
	ctx, h, finish, err := l.beginRun(context.Background(), "s1", "autopilot", "Preserve newer work.")
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil, nil)
	h.registry = l.registry()
	if _, err = h.writer.Append(episodic.Plan, p); err != nil {
		t.Fatal(err)
	}
	ctx, brief, err := l.prepareRecovery(ctx, "s1", p, s, 0)
	if err != nil {
		t.Fatal(err)
	}
	mode := ModeByName("autopilot")
	out, runErr := l.runStep(ctx, h.writer, "s1", "Repair only the current step.", mode, p, 0, nil, StepPrompt{Step: p.Steps[0], Context: brief})
	events, err := episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	return out, runErr, events
}

func TestRecoveryTruncationDoesNotSpendRemainingActionIterations(t *testing.T) {
	var replies []map[string]any
	for i := 1; i <= 6; i++ {
		replies = append(replies, toolCall("read", fmt.Sprintf(`{"path":"world.js","offset":%d,"limit":1}`, i)))
	}
	replies = append(replies, emptyLengthReply(), toolCall("write", `{"path":"world.js","content":"small repair"}`), textReply("ready for check"))
	m := newScriptedModel(replies...)
	defer m.srv.Close()
	out, err, events := runRecoveryAttempt(t, m, "medium")
	if err != nil || out != "ready for check" {
		t.Fatalf("fallback never repaired: %q %v", out, err)
	}
	if len(m.bodies) != 9 {
		t.Fatalf("calls = %d, want 9", len(m.bodies))
	}
	if !strings.Contains(m.bodies[7], `"enable_thinking":false`) || !strings.Contains(m.bodies[7], "one small") {
		t.Fatal("missing actionable off-thinking fallback")
	}
	if !strings.Contains(m.bodies[4], "RECOVERY CHECKPOINT") {
		t.Fatal("no early diagnosis-stall coaching")
	}
	nudges, fallbacks := 0, 0
	for _, ev := range events {
		if ev.Type != episodic.Note {
			continue
		}
		var n struct{ Kind string }
		json.Unmarshal(ev.Payload, &n)
		if n.Kind == "recovery_checkpoint" {
			nudges++
		}
		if n.Kind == "output_retry" {
			fallbacks++
		}
	}
	if nudges != 1 || fallbacks != 1 {
		t.Fatalf("unbounded/absent notes: nudges=%d fallbacks=%d", nudges, fallbacks)
	}
}

func TestRecoveryTruncationReissuesAreBounded(t *testing.T) {
	m := newScriptedModel(emptyLengthReply())
	defer m.srv.Close()
	_, err, _ := runRecoveryAttempt(t, m, "xhigh")
	if err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("want precise output-limit stop, got %v", err)
	}
	if len(m.bodies) != 2 {
		t.Fatalf("calls = %d, want original + one reissue", len(m.bodies))
	}
}

func TestRecoveryReaderSearchAndByteIdentity(t *testing.T) {
	ws, archive := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(ws, "world.js"), []byte("broken"), 0600)
	copies, err := preservePlanFiles(ws, archive, &Plan{Steps: []PlanStep{{Files: []string{"world.js"}}}})
	if err != nil {
		t.Fatal(err)
	}
	old := episodic.Event{ID: "evt_000001", Type: episodic.ToolCall, Payload: json.RawMessage(`{"name":"write","args":{"content":"function rawBlock OLD"}}`)}
	current := episodic.Event{ID: "evt_000003", Type: episodic.ToolCall, Payload: json.RawMessage(`{"name":"write","args":{"content":"function rawBlock NEW"}}`)}
	_, r := recoveryHistory([]episodic.Event{old, {ID: "evt_000002", Type: episodic.Plan}, current}, archive)
	r.workspace = ws
	r.copies[copies[0].Blob] = copies[0]
	out, err := r.Execute(context.Background(), json.RawMessage(`{"query":"rawBlock"}`))
	if err != nil || !strings.Contains(out, current.ID) || strings.Contains(out, old.ID) {
		t.Fatalf("search scope: %s %v", out, err)
	}
	args, _ := json.Marshal(map[string]any{"id": copies[0].Blob})
	out, err = r.Execute(context.Background(), args)
	if err != nil || !strings.Contains(out, `"matches_current":true`) || !strings.Contains(out, `"total_bytes":6`) {
		t.Fatalf("identity: %s %v", out, err)
	}
	os.WriteFile(filepath.Join(ws, "world.js"), []byte("repaired"), 0600)
	out, err = r.Execute(context.Background(), args)
	if err != nil || !strings.Contains(out, `"matches_current":false`) {
		t.Fatalf("stale equality: %s %v", out, err)
	}
	args, _ = json.Marshal(map[string]any{"id": copies[0].Blob, "offset": 100000})
	if _, err = r.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "0..6") {
		t.Fatalf("missing byte bounds: %v", err)
	}
	for _, bad := range []string{`{}`, `{"id":"x","query":"rawBlock"}`, `{"query":" "}`, `{"query":"rawBlock","offset":1}`} {
		if _, err := r.Execute(context.Background(), json.RawMessage(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestRecoveryReportCountsAndHistoricalVerdict(t *testing.T) {
	p := &Plan{Title: "voxel", Steps: make([]PlanStep, 8)}
	s := NewSupervisor(p)
	for i := 0; i < 2; i++ {
		s.Steps[i].Status = "needs_reverify"
		s.Steps[i].Verdict = &Verdict{Pass: true, Check: "node test", Evidence: "previous output"}
	}
	s.Steps[2].Status = "blocked"
	rep := reportFromSupervisor(s, "plan")
	if rep.Done != 0 || rep.Failed != 1 || rep.NeedsReverify != 2 || rep.Pending != 5 || rep.Skipped != 0 {
		t.Fatalf("incorrect counts: %+v", rep)
	}
	if !strings.Contains(rep.Steps[0].Summary, "Previously passed") {
		t.Fatalf("unqualified stale proof: %+v", rep.Steps[0])
	}
	results := make([]StepResult, 8)
	for i, st := range s.Steps {
		results[i] = StepResult{Step: st.Title, Status: statusFor(st.Status), Summary: stepSummary(st)}
	}
	text := renderReport(p, results, true)
	for _, want := range []string{"2 awaiting recheck", "5 not started", "1 blocked", "Previously passed"} {
		if !strings.Contains(text, want) {
			t.Errorf("report missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "verified:") || strings.Contains(text, "7 skipped") {
		t.Fatalf("misleading report: %s", text)
	}
}
