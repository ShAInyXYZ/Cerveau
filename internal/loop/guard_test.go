package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
)

func setup(t *testing.T, respond func(call int) map[string]any) (*Loop, string, *int) {
	t.Helper()
	tmp := t.TempDir()
	calls := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(respond(*calls))
	}))
	t.Cleanup(srv.Close)

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)

	os.WriteFile(filepath.Join(tmp, "f.txt"), []byte("data"), 0o644)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	return l, eventsPath, calls
}

func toolCallReply(id string) map[string]any {
	return map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"id":   id,
					"type": "function",
					"function": map[string]string{
						"name":      "read",
						"arguments": `{"path":"f.txt"}`,
					},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
	}
}

// A tool whose output changes every call — models legitimately re-run the same
// command (e.g. `npm run build`) while fixing things, and each run reports a
// different error. That is progress, NOT a stuck loop, and must reach the
// iteration cap rather than being killed by loop detection.
type changingTool struct{ n int }

func (c *changingTool) Name() string           { return "runner" }
func (c *changingTool) Description() string    { return "returns changing output" }
func (c *changingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (c *changingTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	c.n++
	return "error variant " + string(rune('A'+c.n)), nil
}

func TestSameCallDifferentResultIsNotALoop(t *testing.T) {
	tmp := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "c", "type": "function",
					"function": map[string]string{"name": "runner", "arguments": `{}`},
				}}},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)
	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: &changingTool{}, RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	res, err := l.Run(context.Background(), "s1", "keep going", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	// must NOT be killed by the loop detector — same command, changing output
	if res.StopReason == StopLoop {
		t.Fatalf("loop detector tripped on changing output — should have allowed progress")
	}
	if res.StopReason != StopIterations {
		t.Fatalf("stop = %s, want %s (ran to iteration cap)", res.StopReason, StopIterations)
	}
}

func TestLoopDetectionTrips(t *testing.T) {
	n := 0
	l, eventsPath, _ := setup(t, func(call int) map[string]any {
		n++
		return toolCallReply("call_1")
	})
	res, err := l.Run(context.Background(), "s1", "loop please", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != StopLoop {
		t.Fatalf("stop = %s, want %s", res.StopReason, StopLoop)
	}
	if !res.Capped {
		t.Fatal("expected capped result")
	}
	events, _ := episodic.Replay(eventsPath)
	var guardErr *episodic.Event
	for i, ev := range events {
		if ev.Type == episodic.Err {
			guardErr = &events[i]
		}
	}
	if guardErr == nil {
		t.Fatal("no guard error event recorded")
	}
	var p struct {
		Class string `json:"class"`
		Stop  string `json:"stop"`
	}
	json.Unmarshal(guardErr.Payload, &p)
	if p.Class != "guard" || p.Stop != StopLoop {
		t.Fatalf("guard event = %+v", p)
	}
}

func TestTokenBudgetTrips(t *testing.T) {
	l, _, _ := setup(t, func(call int) map[string]any {
		r := toolCallReply("call_1")
		r["usage"] = map[string]int{"prompt_tokens": 10, "completion_tokens": 20000}
		return r
	})
	res, err := l.Run(context.Background(), "s1", "burn tokens", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != StopTokens && res.StopReason != StopLoop {
		t.Fatalf("stop = %s, want guard_tokens (or loop guard first)", res.StopReason)
	}
}

func TestFinalAnswerStopReason(t *testing.T) {
	l, _, _ := setup(t, func(call int) map[string]any {
		return map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "done"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 3},
		}
	})
	res, err := l.Run(context.Background(), "s1", "hi", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != StopFinalAnswer || res.Capped {
		t.Fatalf("res = %+v", res)
	}
}
