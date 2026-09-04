package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

func TestLatestPlan(t *testing.T) {
	tmp := t.TempDir()
	eventsPath := filepath.Join(tmp, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.MsgUser, map[string]string{"text": "let's plan"})
	wr.Append(episodic.Plan, map[string]any{
		"title": "Refactor auth",
		"steps": []map[string]any{
			{"title": "extract middleware", "detail": "move auth to its own package", "files": []string{"auth.go"}},
			{"title": "add tests"},
		},
		"autonomy_budget": "low",
	})
	wr.Close()

	plan, evtID, err := LatestPlan(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Title != "Refactor auth" || len(plan.Steps) != 2 || plan.Steps[0].Files[0] != "auth.go" {
		t.Fatalf("plan = %+v", plan)
	}
	if evtID == "" {
		t.Fatal("no plan event id")
	}
}

func TestAutopilotFreshWindow(t *testing.T) {
	tmp := t.TempDir()
	var firstReq []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if firstReq == nil {
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			firstReq = body
		}
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "step done"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.MsgUser, map[string]string{"text": "SECRET-CHATTER-MARKER old discussion"})
	wr.Append(episodic.MsgAssistant, map[string]any{"text": "old reply"})
	wr.Append(episodic.Plan, map[string]any{
		"title": "PLAN-MARKER refactor",
		"steps": []map[string]any{{"title": "do the thing"}},
	})
	wr.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	res, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Reply, "Autopilot report") || !strings.Contains(res.Reply, "1 done") {
		t.Fatalf("reply = %q", res.Reply)
	}
	if firstReq == nil {
		t.Fatal("no llm request captured")
	}
	req := string(firstReq)
	if !strings.Contains(req, "PLAN-MARKER") {
		t.Fatal("plan payload missing from fresh window")
	}
	if strings.Contains(req, "SECRET-CHATTER-MARKER") {
		t.Fatal("chat history leaked into the autopilot fresh window")
	}

	events, _ := episodic.Replay(eventsPath)
	var cp *episodic.Event
	for i, ev := range events {
		if ev.Type == episodic.Checkpoint {
			cp = &events[i]
		}
	}
	if cp == nil {
		t.Fatal("no checkpoint event")
	}
	var payload struct {
		Status string `json:"status"`
	}
	json.Unmarshal(cp.Payload, &payload)
	if payload.Status != "done" {
		t.Fatalf("checkpoint = %+v", payload)
	}
}

func TestAutopilotHandbackOnFailure(t *testing.T) {
	tmp := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id":   "c1",
						"type": "function",
						"function": map[string]string{
							"name":      "read",
							"arguments": `{"path":"missing.txt"}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.Plan, map[string]any{
		"title": "failing plan",
		"steps": []map[string]any{{"title": "will fail"}, {"title": "never runs"}},
	})
	wr.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	res, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "plan_drift_handback" {
		t.Fatalf("stop = %s", res.StopReason)
	}
	if !strings.Contains(res.Reply, "1 failed") || !strings.Contains(res.Reply, "1 skipped") {
		t.Fatalf("reply = %q", res.Reply)
	}
}

// A plan step's window must be COMPRESSED like a chat turn's. runStep used
// to send raw items, so a long step grew until the request exceeded the
// model's context and the run died with "exceeds the available context size".
func TestStepWindowIsCompressed(t *testing.T) {
	mgr := window.NewManager(4000, 500, window.CounterFunc(func(_ context.Context, s string) int {
		return len(s) / 4
	}))
	items := []window.Item{{Msg: llm.Message{Role: "system", Content: "sys"}, Kind: "system"}}
	for i := 0; i < 200; i++ {
		items = append(items, window.Item{
			Msg:  llm.Message{Role: "tool", Content: strings.Repeat("x", 4000)},
			Kind: "tool", EvtID: "evt_" + strconv.Itoa(i),
		})
	}
	raw := 0
	for _, it := range items {
		raw += len(it.Msg.Content) / 4
	}
	if raw <= 4000 {
		t.Fatalf("test setup should overflow the budget, got %d", raw)
	}
	msgs, rep := mgr.Build(context.Background(), items)
	sent := 0
	for _, m := range msgs {
		sent += len(m.Content) / 4
	}
	if sent >= raw {
		t.Fatalf("window did not compress: sent %d of raw %d", sent, raw)
	}
	if rep.Demoted == 0 && rep.Trimmed == 0 {
		t.Fatal("nothing was demoted or trimmed")
	}
}

// A write to a file the committed plan never declared must produce a visible
// note — architecture drift (extra files) was silent before, and the model
// split one-file builds into modules without anyone noticing until runtime.
func TestOutOfPlanNote(t *testing.T) {
	p := &Plan{Title: "game", Steps: []PlanStep{
		{Title: "build", Files: []string{"snake/index.html"}},
	}}
	// declared file: no note
	if n := outOfPlanNote(p, "write", []byte(`{"path":"snake/index.html","content":"x"}`)); n != "" {
		t.Errorf("declared file should be silent, got %q", n)
	}
	// undeclared file: note names both the file and the plan's files
	n := outOfPlanNote(p, "write", []byte(`{"path":"snake/game.js","content":"x"}`))
	if !strings.Contains(n, "snake/game.js") || !strings.Contains(n, "not in the committed plan") {
		t.Errorf("undeclared write should be flagged, got %q", n)
	}
	// non-write tools: never a note
	if n := outOfPlanNote(p, "read", []byte(`{"path":"other.js"}`)); n != "" {
		t.Errorf("read should never be flagged, got %q", n)
	}
	// no plan, or a plan with no declared files: silent
	if n := outOfPlanNote(nil, "write", []byte(`{"path":"a.js"}`)); n != "" {
		t.Errorf("nil plan should be silent, got %q", n)
	}
	if n := outOfPlanNote(&Plan{Title: "x"}, "write", []byte(`{"path":"a.js"}`)); n != "" {
		t.Errorf("plan without files should be silent, got %q", n)
	}
}

// A plan written as a FILE (the model's stubborn habit) must be auto-committed
// as a structured plan event, so the plan card and the Planner RFX see it —
// harness translation instead of prompt pleading.
func TestAutoCommitPlanFile(t *testing.T) {
	dir := t.TempDir()
	wr, err := episodic.Open(dir + "/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	md := "# Chess Build\n## Engine\nWrite `src/engine.ts`\n## Renderer\nBoard and pieces\n"
	args := []byte(`{"path":"docs/plan.md","content":` + strconv.Quote(md) + `}`)

	plan, note := autoCommitPlanFile(wr, "write", args)
	if plan == nil {
		t.Fatal("plan-like write should auto-commit")
	}
	if plan.Title != "Chess Build" || len(plan.Steps) != 2 {
		t.Errorf("parsed plan wrong: %+v", plan)
	}
	if note == "" || !strings.Contains(note, "plan") {
		t.Errorf("the auto-commit must be disclosed: %q", note)
	}

	// and it must be discoverable exactly like a hand-committed plan
	got, _, err := LatestPlan(dir + "/events.jsonl")
	if err != nil || got == nil || got.Title != "Chess Build" {
		t.Fatalf("LatestPlan should find the auto-committed plan: %+v %v", got, err)
	}

	// ordinary writes never auto-commit
	if p, _ := autoCommitPlanFile(wr, "write", []byte(`{"path":"main.go","content":"package main"}`)); p != nil {
		t.Error("code writes must not commit plans")
	}
}
