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

func TestLoopToolCallThenAnswer(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "hello.txt")
	os.WriteFile(target, []byte("file-content-123"), 0o644)

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("content-type", "application/json")
		if calls == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []any{map[string]any{
							"id":   "call_1",
							"type": "function",
							"function": map[string]string{
								"name":      "read",
								"arguments": `{"path":"hello.txt"}`,
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "the file says: file-content-123"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	sessDir := filepath.Join(tmp, "sessions")
	os.MkdirAll(sessDir, 0o755)
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	res, err := l.Run(context.Background(), "s1", "what is in hello.txt?", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.Reply != "the file says: file-content-123" {
		t.Fatalf("reply = %q", res.Reply)
	}
	if res.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", res.Iterations)
	}

	events, err := episodic.Replay(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	var types []episodic.EventType
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	want := []episodic.EventType{
		episodic.MsgUser, episodic.MsgAssistant, episodic.ToolCall,
		episodic.ToolResult, episodic.MsgAssistant, episodic.TurnClose,
	}
	if len(types) != len(want) {
		t.Fatalf("event types = %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("event %d = %s, want %s (all: %v)", i, types[i], want[i], types)
		}
	}

	var resultPayload struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(events[3].Payload, &resultPayload); err != nil {
		t.Fatal(err)
	}
	if !resultPayload.OK || resultPayload.Output != "file-content-123" {
		t.Fatalf("tool result = %+v", resultPayload)
	}
}
