package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
)

func TestLLMTransientRetriedThenSucceeds(t *testing.T) {
	tmp := t.TempDir()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("content-type", "application/json")
		if calls <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "502 bad gateway"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "recovered"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	res, err := l.Run(context.Background(), "s1", "hi", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.Reply != "recovered" || calls != 3 {
		t.Fatalf("res = %+v, calls = %d", res, calls)
	}
	events, _ := episodic.Replay(eventsPath)
	retries := 0
	for _, ev := range events {
		if ev.Type == episodic.Err && strings.Contains(string(ev.Payload), "retrying") {
			retries++
		}
	}
	if retries != 2 {
		t.Fatalf("retry events = %d, want 2", retries)
	}
}

func TestLLMFatalNoRetry(t *testing.T) {
	tmp := t.TempDir()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "invalid request: bad grammar"},
		})
	}))
	defer srv.Close()

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	_, err := l.Run(context.Background(), "s1", "hi", "discussion")
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (fatal = no retry)", calls)
	}
}

func TestMalformedArgsNotExecuted(t *testing.T) {
	tmp := t.TempDir()
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
							"id":   "c1",
							"type": "function",
							"function": map[string]string{
								"name":      "read",
								"arguments": `{path: missing quotes}`,
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
				"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "corrected"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	os.WriteFile(filepath.Join(tmp, "f.txt"), []byte("data"), 0o644)
	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	res, err := l.Run(context.Background(), "s1", "hi", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.Reply != "corrected" {
		t.Fatalf("res = %+v", res)
	}
	events, _ := episodic.Replay(eventsPath)
	var malformed bool
	for _, ev := range events {
		if ev.Type == episodic.ToolResult && strings.Contains(string(ev.Payload), "malformed tool call") {
			malformed = true
		}
	}
	if !malformed {
		t.Fatal("no malformed tool result recorded")
	}
}

func TestSameToolThresholdTrips(t *testing.T) {
	g := newTurnGuard(0)
	var tripped string
	var hit bool
	for i := 0; i < 3; i++ {
		tripped, hit = g.toolError("read")
	}
	if !hit || !strings.Contains(tripped, "read") {
		t.Fatalf("same-tool threshold not tripped: %v %q", hit, tripped)
	}
}

func TestTotalThresholdTrips(t *testing.T) {
	g := newTurnGuard(0)
	names := []string{"read", "grep", "bash", "edit", "write"}
	var hit bool
	var tripped string
	for _, n := range names {
		tripped, hit = g.toolError(n)
	}
	if !hit || !strings.Contains(tripped, "total") {
		t.Fatalf("total threshold not tripped: %v %q", hit, tripped)
	}
}

func TestErrorCardShape(t *testing.T) {
	card := errorCard(ErrClassArgs, "read failed", "file not found", "2 tries", "check the path")
	for _, k := range []string{"class", "what", "why", "tried", "options", "proposed_fix"} {
		if _, ok := card[k]; !ok {
			t.Fatalf("card missing %q", k)
		}
	}
	opts, _ := card["options"].([]string)
	if len(opts) != 4 {
		t.Fatalf("options = %v", opts)
	}
}
