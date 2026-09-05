package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cerveau/internal/config"
	"cerveau/internal/llm"
	"cerveau/internal/loop"
	"cerveau/internal/session"
	"cerveau/internal/tools"
)

type runAPIFixture struct {
	a      *API
	l      *loop.Loop
	sid    string
	server *httptest.Server
}

func newRunAPIFixture(t *testing.T, model http.HandlerFunc) *runAPIFixture {
	t.Helper()
	modelServer := httptest.NewServer(model)
	t.Cleanup(modelServer.Close)
	root := t.TempDir()
	store, err := session.NewFSStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.CreateInWorkspace("control-test", root)
	if err != nil {
		t.Fatal(err)
	}
	a := New(&config.Config{}, store)
	registry := tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(root), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewAskUser(a.QuestionBroker(), nil), RiskTier: tools.RiskSafe},
	)
	l := loop.New(llm.NewClient(modelServer.URL), registry, a.Writer, store.EventsPath, nil)
	l.SetWorkspaceFunc(func(string) string { return root })
	a.SetLoop(l)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/commands", a.Command)
	mux.HandleFunc("POST /api/sessions/{id}/chat", a.Chat)
	mux.HandleFunc("POST /api/sessions/{id}/pause", a.Pause)
	mux.HandleFunc("POST /api/sessions/{id}/resume", a.Resume)
	mux.HandleFunc("POST /api/sessions/{id}/kill", a.Kill)
	mux.HandleFunc("POST /api/sessions/{id}/steer", a.Steer)
	mux.HandleFunc("POST /api/sessions/{id}/answer", a.Answer)
	mux.HandleFunc("GET /api/sessions/{id}/question", a.PendingQuestion)
	mux.HandleFunc("GET /api/sessions/{id}/state", a.SessionState)
	server := httptest.NewServer(mux)
	f := &runAPIFixture{a: a, l: l, sid: s.ID, server: server}
	t.Cleanup(func() {
		l.Kill(s.ID)
		deadline := time.Now().Add(2 * time.Second)
		for len(l.RunningSessions()) != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		server.Close()
		a.wmu.Lock()
		for _, wr := range a.writers {
			_ = wr.Close()
		}
		a.wmu.Unlock()
	})
	return f
}

func (f *runAPIFixture) post(t *testing.T, path string, body any) (int, map[string]json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(f.server.URL+"/api/sessions/"+f.sid+"/"+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, result
}

func (f *runAPIFixture) start(t *testing.T, id string) *loop.RunState {
	t.Helper()
	status, body := f.post(t, "commands", map[string]any{"command_id": id, "kind": "chat", "mode": "discussion", "text": "hello"})
	if status != 202 {
		t.Fatalf("start: %d %s", status, body)
	}
	var st loop.RunState
	if err := json.Unmarshal(body["run"], &st); err != nil {
		t.Fatal(err)
	}
	return &st
}

func (f *runAPIFixture) wait(t *testing.T, status string) *loop.RunState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := f.l.RunStateOf(f.sid); st != nil && st.Status == status {
			return st
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiting for %s: %+v", status, f.l.RunStateOf(f.sid))
	return nil
}

func waitModel(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("model request not reached")
	}
}

func TestHTTPRunControlsRejectStaleOwnersAndDeduplicate(t *testing.T) {
	entered := make(chan struct{}, 16)
	f := newRunAPIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
	})
	st := f.start(t, "first")
	waitModel(t, entered)
	if code, _ := f.post(t, "pause", map[string]any{}); code != 400 {
		t.Fatalf("untargeted pause: %d", code)
	}
	control := func(action, id, run string, version uint64) int {
		code, _ := f.post(t, action, map[string]any{"control_id": id, "run_id": run, "control_version": version})
		return code
	}
	if code := control("pause", "pause-1", st.ID, 0); code != 200 {
		t.Fatalf("pause: %d", code)
	}
	f.wait(t, "paused")
	if code := control("pause", "pause-1", st.ID, 0); code != 200 {
		t.Fatalf("duplicate pause: %d", code)
	}
	if code, _ := f.post(t, "commands", map[string]any{"command_id": "busy", "kind": "chat", "mode": "discussion", "text": "other"}); code != 409 {
		t.Fatalf("paused run lost ownership: %d", code)
	}
	if code := control("resume", "stale-resume", st.ID, 0); code != 409 {
		t.Fatalf("stale control version: %d", code)
	}
	if code := control("resume", "resume-1", st.ID, 1); code != 200 {
		t.Fatalf("resume: %d", code)
	}
	waitModel(t, entered)
	if code := control("pause", "pause-1", st.ID, 0); code != 200 {
		t.Fatalf("late duplicate pause: %d", code)
	}
	if current := f.l.RunStateOf(f.sid); current.Status != "running" || current.ControlVersion != 2 {
		t.Fatalf("late pause undid resume: %+v", current)
	}
	if code := control("kill", "kill-1", st.ID, 2); code != 200 {
		t.Fatalf("kill: %d", code)
	}
	f.wait(t, "cancelled")
	newer := f.start(t, "second")
	waitModel(t, entered)
	if code := control("pause", "old-owner", st.ID, 3); code != 409 {
		t.Fatalf("stale run: %d", code)
	}
	if code := control("kill", "kill-1", st.ID, 2); code != 200 {
		t.Fatalf("completed control retry: %d", code)
	}
	if current := f.l.RunStateOf(f.sid); current.ID != newer.ID || current.Status != "running" {
		t.Fatalf("old control affected new owner: %+v", current)
	}
}

func TestHTTPObserverDisconnectDoesNotCancelAcceptedWorker(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	modelCancelled := make(chan struct{}, 1)
	f := newRunAPIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
			modelCancelled <- struct{}{}
			return
		case <-release:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/api/sessions/"+f.sid+"/chat", strings.NewReader(`{"text":"hello","mode":"discussion"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "observer-command")
	done := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	waitModel(t, entered)
	accepted := f.l.RunStateOf(f.sid)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer did not disconnect")
	}
	select {
	case <-modelCancelled:
		t.Fatal("HTTP disconnect cancelled the model worker")
	case <-time.After(25 * time.Millisecond):
	}
	if current := f.l.RunStateOf(f.sid); current.ID != accepted.ID || current.Status != "running" {
		t.Fatalf("worker lost after disconnect: %+v", current)
	}
	close(release)
	completed := f.wait(t, "completed")
	if completed.ID != accepted.ID || completed.Result == nil || completed.Result.Reply != "done" {
		t.Fatalf("detached worker result: %+v", completed)
	}
	// A new observer sees the same terminal result, not an invented cancellation.
	resp, err := http.Get(f.server.URL + "/api/sessions/" + f.sid + "/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state struct {
		Run     *loop.RunState `json:"run"`
		Running bool           `json:"running"`
		Cursor  string         `json:"cursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Run == nil || state.Run.ID != accepted.ID || state.Run.Status != "completed" || state.Running || state.Cursor == "" {
		t.Fatalf("reconnected projection: %+v", state)
	}
}

func TestHTTPQuestionAnswerRequiresOwningRunAndQuestion(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	f := newRunAPIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"ask-1","type":"function","function":{"name":"ask_user","arguments":"{\"question\":\"Which option?\",\"options\":[\"one\",\"two\"]}"}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-release:
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	})
	st := f.start(t, "question")
	f.wait(t, "waiting_user")
	var question struct {
		ID    string `json:"id"`
		RunID string `json:"run_id"`
	}
	deadline := time.Now().Add(time.Second)
	for question.ID == "" && time.Now().Before(deadline) {
		resp, err := http.Get(f.server.URL + "/api/sessions/" + f.sid + "/question")
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(resp.Body).Decode(&question)
		resp.Body.Close()
		if question.ID == "" {
			time.Sleep(time.Millisecond)
		}
	}
	if question.ID == "" || question.RunID != st.ID {
		t.Fatalf("question lacks owner: %+v", question)
	}
	answer := map[string]any{"question_id": question.ID, "run_id": "older-run", "answer": "one"}
	if code, _ := f.post(t, "answer", answer); code != 409 {
		t.Fatalf("stale answer: %d", code)
	}
	answer["run_id"] = st.ID
	if code, _ := f.post(t, "answer", answer); code != 200 {
		t.Fatalf("answer: %d", code)
	}
	if code, _ := f.post(t, "answer", answer); code != 409 {
		t.Fatalf("duplicate answer: %d", code)
	}
	close(release)
	f.wait(t, "completed")
	if code, _ := f.post(t, "answer", answer); code != 409 {
		t.Fatalf("terminal answer: %d", code)
	}
}
