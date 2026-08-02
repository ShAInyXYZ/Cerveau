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
	"cerveau/internal/window"
	"cerveau/internal/tools"
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
