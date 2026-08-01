package loop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/memory"
	"cerveau/internal/tools"
)

func TestTurnCloseGrammarGenerates(t *testing.T) {
	g, err := tools.SchemaToGBNF(turnCloseSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g, "promotion_candidates") || !strings.Contains(g, "open_loops") {
		t.Fatalf("grammar incomplete:\n%s", g)
	}
}

type boundaryFake struct {
	mu      sync.Mutex
	docs    []memory.Doc
	distill []byte
}

func TestBoundaryHookPromotes(t *testing.T) {
	tmp := t.TempDir()
	fake := &boundaryFake{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if strings.Contains(r.URL.Path, "search") {
			json.NewEncoder(w).Encode(map[string]any{"hits": []any{}, "found": 0})
			return
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "documents") {
			var d memory.Doc
			json.NewDecoder(r.Body).Decode(&d)
			fake.mu.Lock()
			fake.docs = append(fake.docs, d)
			fake.mu.Unlock()
			json.NewEncoder(w).Encode(d)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer ts.Close()

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		if strings.Contains(string(body), `"grammar":"ws ::=`) {
			fake.mu.Lock()
			fake.distill = body
			fake.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{
					"message":       map[string]string{"role": "assistant", "content": `{"summary":"planned the refactor","decisions":["use interface"],"promotion_candidates":[{"content":"user prefers interfaces","category":"preference"}],"open_loops":[]}`},
					"finish_reason": "stop",
				}},
				"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 30},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "done"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	l.SetCurator(memory.NewCurator(memory.NewTSClient(ts.URL, "k"), filepath.Join(tmp, "p.jsonl"), func() bool { return true }))

	if _, err := l.Run(context.Background(), "s1", "do it", "discussion"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := len(fake.docs)
		fake.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.docs) != 1 {
		t.Fatalf("promoted docs = %d", len(fake.docs))
	}
	doc := fake.docs[0]
	if doc.Content != "user prefers interfaces" || doc.Category != "preference" || doc.MemoryType != "semantic" {
		t.Fatalf("doc = %+v", doc)
	}
	if len(fake.distill) == 0 {
		t.Fatal("no distill call with grammar seen")
	}

	events, _ := episodic.Replay(eventsPath)
	var summary *episodic.Event
	for i, ev := range events {
		if ev.Type == episodic.Note {
			var p struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil && p.Kind == "turn_summary" {
				summary = &events[i]
			}
		}
	}
	if summary == nil {
		t.Fatal("no turn_summary note event")
	}
}

func TestBoundaryHookDistillFailure(t *testing.T) {
	tmp := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		if strings.Contains(string(body), `"grammar":"ws ::=`) {
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{
					"message":       map[string]string{"role": "assistant", "content": "not json at all"},
					"finish_reason": "stop",
				}},
				"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "done"},
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

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"hits": []any{}})
	}))
	defer ts.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	l.SetCurator(memory.NewCurator(memory.NewTSClient(ts.URL, "k"), filepath.Join(tmp, "p.jsonl"), func() bool { return true }))

	res, err := l.Run(context.Background(), "s1", "do it", "discussion")
	if err != nil || res.StopReason != StopFinalAnswer {
		t.Fatalf("res = %+v, %v", res, err)
	}

	// A malformed distill (non-JSON from the model) must DEGRADE GRACEFULLY:
	// the answer already shipped, so turn_close must NOT log an alarming boundary
	// error. It just yields nothing to promote. Give the async hook time to run,
	// then assert no boundary error was written.
	time.Sleep(600 * time.Millisecond)
	events, _ := episodic.Replay(eventsPath)
	for _, ev := range events {
		if ev.Type == episodic.Err && strings.Contains(string(ev.Payload), "boundary") {
			t.Fatal("bad-JSON distill should degrade silently, not log a boundary error")
		}
	}
}
