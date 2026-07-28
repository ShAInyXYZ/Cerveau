package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
)

func setupInterruptLoop(t *testing.T, handler http.HandlerFunc) (*Loop, string) {
	t.Helper()
	tmp := t.TempDir()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)
	os.WriteFile(filepath.Join(tmp, "f.txt"), []byte("data"), 0o644)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	return l, eventsPath
}

func TestSteerRedirects(t *testing.T) {
	var calls int
	release := make(chan struct{})
	l, eventsPath := setupInterruptLoop(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("content-type", "application/json")
		if calls == 1 {
			<-release
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "answer after steer"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	})

	resCh := make(chan *Result, 1)
	go func() {
		res, _ := l.Run(context.Background(), "s1", "first question", "discussion")
		resCh <- res
	}()
	time.Sleep(150 * time.Millisecond)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.MsgUser, map[string]any{"text": "new direction", "steer": true})
	wr.Close()
	if !l.Steer("s1") {
		t.Fatal("no active run to steer")
	}
	close(release)
	res := <-resCh
	if res == nil || res.Reply != "answer after steer" {
		t.Fatalf("res = %+v", res)
	}
	events, _ := episodic.Replay(eventsPath)
	var aborted, steerMsg bool
	for _, ev := range events {
		if ev.Type == episodic.Aborted {
			aborted = true
		}
		if ev.Type == episodic.MsgUser {
			var p struct {
				Steer bool `json:"steer"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil && p.Steer {
				steerMsg = true
			}
		}
	}
	if !aborted || !steerMsg {
		t.Fatalf("aborted=%v steerMsg=%v", aborted, steerMsg)
	}
}

func TestPauseParks(t *testing.T) {
	var calls int
	l, _ := setupInterruptLoop(t, func(w http.ResponseWriter, r *http.Request) {
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
								"arguments": `{"path":"f.txt"}`,
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
				"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
			})
			return
		}
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id":   "c2",
						"type": "function",
						"function": map[string]string{
							"name":      "read",
							"arguments": `{"path":"f.txt"}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	})

	resCh := make(chan *Result, 1)
	go func() {
		res, _ := l.Run(context.Background(), "s1", "work", "discussion")
		resCh <- res
	}()
	time.Sleep(150 * time.Millisecond)
	if !l.Pause("s1") {
		t.Fatal("no active run to pause")
	}
	res := <-resCh
	if res == nil || res.StopReason != "paused" {
		t.Fatalf("res = %+v", res)
	}
}

func TestKillAborts(t *testing.T) {
	release := make(chan struct{})
	l, _ := setupInterruptLoop(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	})
	resCh := make(chan *Result, 1)
	go func() {
		res, _ := l.Run(context.Background(), "s1", "work", "discussion")
		resCh <- res
	}()
	time.Sleep(150 * time.Millisecond)
	if !l.Kill("s1") {
		t.Fatal("no active run to kill")
	}
	close(release)
	res := <-resCh
	if res == nil || res.StopReason != "killed" {
		t.Fatalf("res = %+v", res)
	}
}

func TestAskUserBroker(t *testing.T) {
	ask := tools.NewAskUser(func(ctx context.Context, sessionID, q string, opts []string) (string, error) {
		if sessionID != "s1" || q == "" || len(opts) != 2 {
			t.Fatalf("broker got %s %q %v", sessionID, q, opts)
		}
		return "option A", nil
	}, &tools.SessionContext{SessionID: "s1"})
	out, err := ask.Execute(context.Background(), json.RawMessage(`{"question":"which one?","options":["option A","option B"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "option A" {
		t.Fatalf("out = %q", out)
	}
}
