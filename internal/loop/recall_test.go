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
	"cerveau/internal/memory"
	"cerveau/internal/tools"
)

func TestRecallInjectedInFirstThink(t *testing.T) {
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
				"message":       map[string]string{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2},
		})
	}))
	defer srv.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"found": 1,
			"hits": []any{map[string]any{
				"document": map[string]any{
					"id": "s0:evt_000001", "session_id": "s0", "memory_type": "episodic",
					"evt_id": "evt_000001", "content": "user prefers tabs over spaces",
				},
			}},
		})
	}))
	defer ts.Close()

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	l.SetRecall(memory.NewRecall(memory.NewTSClient(ts.URL, "k"), sessDir, false))

	res, err := l.Run(context.Background(), "s1", "format my code", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulls != 1 {
		t.Fatalf("pulls = %d, want 1", res.Pulls)
	}
	if firstReq == nil || !strings.Contains(string(firstReq), "Recalled memory") {
		t.Fatalf("first request missing recalled memory:\n%s", firstReq)
	}
	if !strings.Contains(string(firstReq), "tabs over spaces") {
		t.Fatalf("recalled content not in window")
	}
}
