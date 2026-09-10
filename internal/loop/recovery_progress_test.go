package loop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/plan"
	"cerveau/internal/tools"
)

func progressFixture(t *testing.T) (string, string, *episodic.Writer, string, PlanStep) {
	t.Helper()
	ws, journal := t.TempDir(), filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(filepath.Join(ws, "world.js"), []byte("retained source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	step := PlanStep{Title: "repair", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "repaired"}}
	ev, err := w.Append(episodic.Plan, &Plan{Steps: []PlanStep{step}})
	if err != nil {
		t.Fatal(err)
	}
	return ws, journal, w, ev.ID, step
}

func observeFixture(t *testing.T, p *recoveryProgress, w *episodic.Writer, name, args, out string) {
	t.Helper()
	tc := llm.ToolCall{ID: "fixture", Function: llm.FunctionCall{Name: name, Arguments: args}}
	if _, err := w.Append(episodic.ToolCall, map[string]any{"id": tc.ID, "name": name, "args": json.RawMessage(args)}); err != nil {
		t.Fatal(err)
	}
	ev, err := w.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": name, "ok": true, "output": out})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.observe(w, tc, ev.ID, out, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryProgressSurvivesReplayAndInvalidatesSourceVersion(t *testing.T) {
	ws, journal, w, id, step := progressFixture(t)
	p := restoreRecoveryProgress(nil, ws, id, 0, step)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("retained source\n")))
	read := fmt.Sprintf("[read {\"path\":\"world.js\",\"sha256\":\"%s\",\"from_line\":1,\"to_line\":1,\"start_offset\":0,\"end_offset\":16,\"total_bytes\":16,\"eof\":true}]\n1\tretained source\n", hash)
	observeFixture(t, p, w, "read", `{"path":"world.js","from_line":1,"to_line":1}`, read)
	events, err := episodic.Replay(journal)
	if err != nil {
		t.Fatal(err)
	}
	restarted := restoreRecoveryProgress(events, ws, id, 0, step)
	for _, want := range []string{"RECOVERY MEMORY", "retained source", "matches current source", hash} {
		if !strings.Contains(restarted.brief(), want) {
			t.Fatalf("missing %q: %s", want, restarted.brief())
		}
	}
	if err = os.WriteFile(filepath.Join(ws, "world.js"), []byte("changed source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := restarted.brief(); !strings.Contains(got, "stale source") || strings.Contains(got, "matches current source") {
		t.Fatal(got)
	}
}

func TestRecoveryProgressIsPlanStepAndCheckBound(t *testing.T) {
	ws, journal, w, id, step := progressFixture(t)
	p := restoreRecoveryProgress(nil, ws, id, 0, step)
	observeFixture(t, p, w, "recovery_read", `{"ref":"evt_000012#args.content","offset":42}`, `{"ref":"evt_000012#args.content","source":"historical useful tail","execution":"unconfirmed"}`)
	events, _ := episodic.Replay(journal)
	if !strings.Contains(restoreRecoveryProgress(events, ws, id, 0, step).brief(), "historical useful tail") {
		t.Fatal("lost recovered source")
	}
	if got := restoreRecoveryProgress(events, ws, id, 1, step).brief(); got != "" {
		t.Fatal("cross-step leak", got)
	}
	changed := step
	changed.Verify = &plan.Verify{Kind: "contains", File: "world.js", Symbol: "different contract"}
	if got := restoreRecoveryProgress(events, ws, id, 0, changed).brief(); got != "" {
		t.Fatal("cross-check leak", got)
	}
	if got := restoreRecoveryProgress(events, ws, "other-plan", 0, step).brief(); got != "" {
		t.Fatal("cross-plan leak", got)
	}
	// A newer plan supersedes older note identities even if the caller asks for the old ID.
	events = append(events, episodic.Event{ID: "new-plan", Type: episodic.Plan})
	if got := restoreRecoveryProgress(events, ws, id, 0, step).brief(); got != "" {
		t.Fatal("superseded plan leak", got)
	}
}

func TestRecoveryProgressBoundsDeduplicatesAndDoesNotInventPass(t *testing.T) {
	ws, journal, w, id, step := progressFixture(t)
	p := restoreRecoveryProgress(nil, ws, id, 0, step)
	for i := 0; i < 50; i++ {
		args, _ := json.Marshal(map[string]any{"ref": fmt.Sprintf("evt_%06d#args.content", i), "offset": 0})
		observeFixture(t, p, w, "recovery_read", string(args), `{"source":"`+strings.Repeat("x", 16000)+`","execution":"tool_succeeded"}`)
	}
	before := len(p.entries)
	observeFixture(t, p, w, "recovery_read", `{"ref":"evt_000049#args.content","offset":0}`, `{"source":"`+strings.Repeat("x", 16000)+`","execution":"tool_succeeded"}`)
	if len(p.entries) != before || before > 24 {
		t.Fatalf("unbounded/duplicate memory %d -> %d", before, len(p.entries))
	}
	events, _ := episodic.Replay(journal)
	got := restoreRecoveryProgress(events, ws, id, 0, step).brief()
	if len(got) > 16000 || !strings.Contains(got, "not verification") || !strings.Contains(got, "excerpt") {
		t.Fatalf("bad memory size %d", len(got))
	}
}

func TestRecoveryProgressRejectsOrphanedAndModifiedEvidence(t *testing.T) {
	ws, journal, w, id, step := progressFixture(t)
	p := restoreRecoveryProgress(nil, ws, id, 0, step)
	observeFixture(t, p, w, "read", `{"path":"world.js"}`, "source evidence")
	events, _ := episodic.Replay(journal)
	for i := range events {
		if events[i].Type == episodic.ToolResult {
			events[i].Payload = json.RawMessage(`{"name":"read","ok":true,"output":"tampered"}`)
		}
	}
	if got := restoreRecoveryProgress(events, ws, id, 0, step).brief(); got != "" {
		t.Fatal("unpaired evidence trusted", got)
	}
}

func TestRecoveryProgressRejectsChangedNavigationReference(t *testing.T) {
	ws, journal, w, id, step := progressFixture(t)
	p := restoreRecoveryProgress(nil, ws, id, 0, step)
	observeFixture(t, p, w, "recovery_read", `{"ref":"evt_000012#args.content","offset":42}`, `{"source":"actual recovered fragment"}`)
	events, _ := episodic.Replay(journal)
	for i := range events {
		if events[i].Type != episodic.Note {
			continue
		}
		var o recoveryObservation
		if json.Unmarshal(events[i].Payload, &o) == nil && o.Kind == "recovery_observation" {
			o.Arguments = `{"ref":"evt_999999#args.content","offset":0}`
			events[i].Payload, _ = json.Marshal(o)
		}
	}
	if got := restoreRecoveryProgress(events, ws, id, 0, step).brief(); got != "" {
		t.Fatal("trusted unpaired navigation args", got)
	}
}

func TestRecoveryRetryModelReceivesAcquiredRegionsAndContinues(t *testing.T) {
	var replies []map[string]any
	for i := 1; i <= 8; i++ {
		replies = append(replies, toolCall("read", fmt.Sprintf(`{"path":"world.js","from_line":%d,"to_line":%d}`, i, i)))
	}
	replies = append(replies, toolCall("edit", `{"path":"world.js","old_string":"broken unique target","new_string":"repaired unique target"}`), textReply("repair ready for committed check"), textReply("next step checked"))
	m := newScriptedModel(replies...)
	defer m.srv.Close()
	l, journal := gateFixture(t, m)
	ws := t.TempDir()
	l.SetWorkspaceFunc(func(string) string { return ws })
	if err := os.WriteFile(filepath.Join(ws, "world.js"), []byte("first\nsecond\nthird\nfourth\nfifth\nsixth\nseventh\nbroken unique target\n"), 0600); err != nil {
		t.Fatal(err)
	}
	l.SetRegistry(tools.NewRegistry(tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe}, tools.Entry{Tool: tools.NewEdit(ws), RiskTier: tools.RiskSensitive}))
	w, err := episodic.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Append(episodic.Plan, &Plan{Title: "durable recovery integration", Steps: []PlanStep{
		{Title: "repair", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "repaired unique target"}},
		{Title: "following step", Files: []string{"other.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "first"}},
	}})
	w.Close()
	if err != nil {
		t.Fatal(err)
	}
	result, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	st, err := l.PlanStateOf("s1")
	if err != nil || !st.Done || result.StopReason == "plan_blocked" {
		t.Fatalf("did not repair/continue: %+v %+v %v", result, st, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bodies) != 11 || !strings.Contains(m.bodies[8], "RECOVERY MEMORY") || !strings.Contains(m.bodies[8], "broken unique target") || !strings.Contains(m.bodies[8], "matches current source") {
		t.Fatalf("retry omitted evidence, calls=%d", len(m.bodies))
	}
	if strings.Contains(m.bodies[10], "RECOVERY MEMORY") {
		t.Fatal("prior step recovery memory leaked into following step")
	}
}
