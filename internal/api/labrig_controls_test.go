package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cerveau/internal/config"
	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/loop"
	"cerveau/internal/plan"
	"cerveau/internal/session"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

// These acceptance cases are opt-in and deliberately retain all evidence.
// They use an existing Core through an observational proxy: no canned replies,
// model configuration changes, production sessions, or installed binaries.
// Every case refuses to reuse its evidence directory.
type labrigModelObservation struct {
	Number     int           `json:"number"`
	Received   time.Time     `json:"received"`
	Sent       time.Time     `json:"sent"`
	Finished   time.Time     `json:"finished"`
	Outcome    string        `json:"outcome"`
	HTTPStatus int           `json:"http_status,omitempty"`
	Messages   []llm.Message `json:"messages"`
	Usage      llm.Usage     `json:"usage"`
}

type labrigModelProbe struct {
	mu       sync.Mutex
	requests []*labrigModelObservation
	sent     chan int
	server   *httptest.Server
	client   *http.Client
}

func newLABRIGModelProbe(t *testing.T, endpoint string) *labrigModelProbe {
	t.Helper()
	return newLABRIGModelProbeWithTimeout(t, endpoint, 110*time.Second)
}

func newLABRIGModelProbeWithTimeout(t *testing.T, endpoint string, timeout time.Duration) *labrigModelProbe {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" {
		t.Fatal("CERVEAU_ACCEPTANCE_MODEL_URL must be an HTTP(S) Core base URL without credentials or query")
	}
	if timeout <= 0 || timeout > 10*time.Minute {
		t.Fatal("acceptance proxy request timeout must be positive and at most the production 10-minute limit")
	}
	p := &labrigModelProbe{sent: make(chan int, 32)}
	p.client = &http.Client{Transport: llm.CoreTransport(), Timeout: timeout}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "acceptance proxy could not read request", 400)
			return
		}
		var request struct {
			Messages []llm.Message `json:"messages"`
		}
		if json.Unmarshal(body, &request) != nil {
			http.Error(w, "acceptance proxy expected JSON", 400)
			return
		}
		p.mu.Lock()
		observation := &labrigModelObservation{Number: len(p.requests) + 1, Received: time.Now().UTC(), Messages: request.Messages}
		p.requests = append(p.requests, observation)
		p.mu.Unlock()
		trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err != nil {
				return
			}
			p.mu.Lock()
			observation.Sent = time.Now().UTC()
			p.mu.Unlock()
			select {
			case p.sent <- observation.Number:
			default:
			}
		}}
		ctx := httptrace.WithClientTrace(r.Context(), trace)
		upstream, err := http.NewRequestWithContext(ctx, r.Method, strings.TrimRight(endpoint, "/")+r.URL.RequestURI(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, "acceptance proxy could not construct request", 500)
			return
		}
		// Credentials are forwarded privately, never retained in observations.
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Authorization", r.Header.Get("Authorization"))
		response, callErr := p.client.Do(upstream)
		if callErr != nil {
			p.mu.Lock()
			observation.Finished, observation.Outcome = time.Now().UTC(), "transport_error"
			if r.Context().Err() != nil {
				observation.Outcome = "cancelled"
			}
			p.mu.Unlock()
			http.Error(w, "acceptance upstream request ended without a response", 502)
			return
		}
		defer response.Body.Close()
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		var decoded struct {
			Usage llm.Usage `json:"usage"`
		}
		_ = json.Unmarshal(data, &decoded)
		p.mu.Lock()
		observation.Finished, observation.Outcome = time.Now().UTC(), "response"
		observation.HTTPStatus, observation.Usage = response.StatusCode, decoded.Usage
		if readErr != nil {
			observation.Outcome = "response_read_error"
		}
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(data)
	}))
	return p
}

func (p *labrigModelProbe) observations() []labrigModelObservation {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]labrigModelObservation, 0, len(p.requests))
	for _, item := range p.requests {
		out = append(out, *item)
	}
	return out
}

func (p *labrigModelProbe) waitSent(t *testing.T) int {
	t.Helper()
	select {
	case n := <-p.sent:
		return n
	case <-time.After(20 * time.Second):
		t.Fatal("unverified: no real upstream request was written within 20 seconds")
		return 0
	}
}

// A fixture sanity check, not real-Core acceptance evidence: proves the proxy
// forwards auth privately, records actual messages/usage, and propagates cancel.
func TestLABRIGModelProbeFixture(t *testing.T) {
	t.Setenv("CRV_MODEL_KEY", "fixture-private-key")
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%v", cancelled), func(t *testing.T) {
			cancelSeen := make(chan struct{}, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-private-key" {
					t.Error("private auth was not forwarded")
				}
				_, _ = io.Copy(io.Discard, r.Body)
				if cancelled {
					<-r.Context().Done()
					cancelSeen <- struct{}{}
					return
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`)
			}))
			defer upstream.Close()
			probe := newLABRIGModelProbe(t, upstream.URL)
			defer probe.server.Close()
			client := llm.NewClient(probe.server.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, err := client.Complete(ctx, []llm.Message{{Role: "user", Content: "fixture message"}}, nil, "", 8)
				done <- err
			}()
			if n := probe.waitSent(t); n != 1 {
				t.Fatalf("request number=%d", n)
			}
			if cancelled {
				cancel()
			}
			select {
			case err := <-done:
				if cancelled != (err != nil) {
					t.Fatalf("cancelled=%v err=%v", cancelled, err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("proxy fixture exceeded its deadline")
			}
			if cancelled {
				select {
				case <-cancelSeen:
				case <-time.After(time.Second):
					t.Fatal("cancellation was not propagated upstream")
				}
			}
			observations := probe.observations()
			if len(observations) != 1 || len(observations[0].Messages) != 1 || observations[0].Messages[0].Content != "fixture message" || observations[0].Sent.IsZero() {
				t.Fatalf("request observation missing: %+v", observations)
			}
			if !cancelled && (observations[0].Usage.PromptTokens != 7 || observations[0].Usage.CompletionTokens != 2) {
				t.Fatalf("usage observation missing: %+v", observations[0])
			}
			retained, err := json.Marshal(observations)
			if err != nil || bytes.Contains(retained, []byte("fixture-private-key")) || bytes.Contains(retained, []byte("Authorization")) {
				t.Fatal("private headers leaked into retained observations")
			}
		})
	}
}

func labrigWriteEvidence(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

type labrigAcceptanceFixture struct {
	a        *API
	l        *loop.Loop
	store    *session.FSStore
	sid      string
	root     string
	ws       string
	server   *httptest.Server
	probe    *labrigModelProbe
	started  time.Time
	deadline time.Time
	evidence map[string]any
	verified bool
}

func newLABRIGAcceptanceFixture(t *testing.T, relative string) *labrigAcceptanceFixture {
	t.Helper()
	endpoint := os.Getenv("CERVEAU_ACCEPTANCE_MODEL_URL")
	if endpoint == "" {
		t.Skip("set CERVEAU_ACCEPTANCE_MODEL_URL and CERVEAU_ACCEPTANCE_ROOT for opt-in real-Core acceptance")
	}
	base := os.Getenv("CERVEAU_ACCEPTANCE_ROOT")
	if !filepath.IsAbs(base) {
		t.Fatal("CERVEAU_ACCEPTANCE_ROOT must be an existing absolute evidence directory")
	}
	if info, err := os.Stat(base); err != nil || !info.IsDir() {
		t.Fatal("acceptance evidence root does not exist")
	}
	root := filepath.Join(base, relative)
	if err := os.MkdirAll(filepath.Dir(root), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatalf("refusing to reuse acceptance evidence directory: %v", err)
	}
	fixtureReady := false
	t.Cleanup(func() {
		if !fixtureReady {
			if err := labrigWriteEvidence(filepath.Join(root, "setup-failure.json"), map[string]any{"case": relative, "verified": false, "reason": "fixture setup did not finish"}); err != nil {
				t.Error(err)
			}
		}
	})
	ws := filepath.Join(root, "workspace")
	if err := os.Mkdir(ws, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewFSStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.CreateInWorkspace("LABRIG acceptance "+relative, ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := labrigWriteEvidence(filepath.Join(root, "identity.json"), map[string]string{"session_id": meta.ID, "case": relative}); err != nil {
		t.Fatal(err)
	}
	a := New(config.Default(), store)
	probe := newLABRIGModelProbe(t, endpoint)
	registry := tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSafe},
	)
	registry.SetWorkspace(ws)
	l := loop.New(llm.NewClient(probe.server.URL), registry, a.Writer, store.EventsPath, nil)
	l.SetWorkspaceFunc(func(string) string { return ws })
	l.SetThinking("off", "low")
	l.SetSampling("strict")
	a.SetLoop(l)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/commands", a.Command)
	mux.HandleFunc("POST /api/sessions/{id}/pause", a.Pause)
	mux.HandleFunc("POST /api/sessions/{id}/resume", a.Resume)
	mux.HandleFunc("POST /api/sessions/{id}/steer", a.Steer)
	mux.HandleFunc("GET /api/sessions/{id}/state", a.SessionState)
	f := &labrigAcceptanceFixture{a: a, l: l, store: store, sid: meta.ID, root: root, ws: ws,
		server: httptest.NewServer(mux), probe: probe, started: time.Now(), evidence: map[string]any{"case": relative, "session_id": meta.ID, "settings": "isolated run: thinking off, sampling strict; Core unchanged"}}
	f.deadline = f.started.Add(110 * time.Second)
	stopDeadline := make(chan struct{})
	go func() {
		timer := time.NewTimer(time.Until(f.deadline))
		defer timer.Stop()
		select {
		case <-timer.C:
			l.Kill(meta.ID)
		case <-stopDeadline:
		}
	}()
	t.Cleanup(func() {
		close(stopDeadline)
		l.Kill(meta.ID)
		deadline := time.Now().Add(5 * time.Second)
		for len(l.RunningSessions()) > 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		l.WaitBackground(time.Second)
		f.server.Close()
		probe.server.Close()
		observations := probe.observations()
		f.evidence["verified"] = f.verified && !t.Failed()
		f.evidence["elapsed_ms"] = time.Since(f.started).Milliseconds()
		f.evidence["model_requests"] = len(observations)
		f.evidence["model_name"] = os.Getenv("CRV_MODEL_NAME")
		if p, err := l.Snapshot(meta.ID); err == nil {
			f.evidence["final_run"], f.evidence["cursor"] = p.Run, p.Cursor
			calls, results := labrigToolCounts(p.Events)
			f.evidence["tool_calls"], f.evidence["tool_results"] = calls, results
			f.evidence["control_receipts"] = labrigControlReceipts(p.Events)
		}
		a.wmu.Lock()
		for _, wr := range a.writers {
			_ = wr.Close()
		}
		a.wmu.Unlock()
		if err := labrigWriteEvidence(filepath.Join(root, "requests.json"), observations); err != nil {
			t.Error(err)
		}
		if err := labrigWriteEvidence(filepath.Join(root, "evidence.json"), f.evidence); err != nil {
			t.Error(err)
		}
		t.Logf("LABRIG evidence retained: case=%s verified=%v session=%s elapsed_ms=%d evidence=%s", relative, f.verified && !t.Failed(), meta.ID, time.Since(f.started).Milliseconds(), filepath.Join(root, "evidence.json"))
	})
	fixtureReady = true
	return f
}

func labrigToolCounts(events []episodic.Event) (calls, results int) {
	for _, ev := range events {
		if ev.Type == episodic.ToolCall {
			calls++
		}
		if ev.Type == episodic.ToolResult {
			results++
		}
	}
	return
}

func labrigControlReceipts(events []episodic.Event) []json.RawMessage {
	var receipts []json.RawMessage
	for _, ev := range events {
		if ev.Type != episodic.Note {
			continue
		}
		var note struct{ Kind string }
		if json.Unmarshal(ev.Payload, &note) == nil && note.Kind == "run_control" {
			receipts = append(receipts, ev.Payload)
		}
	}
	return receipts
}

func (f *labrigAcceptanceFixture) post(t *testing.T, route string, body any, want int) map[string]json.RawMessage {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/api/sessions/"+f.sid+"/"+route, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s HTTP=%d expected=%d response=%s", route, resp.StatusCode, want, decoded)
	}
	return decoded
}

func (f *labrigAcceptanceFixture) state(t *testing.T) *loop.Projection {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(f.server.URL + "/api/sessions/" + f.sid + "/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var p loop.Projection
	if resp.StatusCode != 200 {
		t.Fatalf("state HTTP=%d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func (f *labrigAcceptanceFixture) waitStatus(t *testing.T, status string, timeout time.Duration) *loop.Projection {
	t.Helper()
	deadline := time.Now().Add(timeout)
	if f.deadline.Before(deadline) {
		deadline = f.deadline
	}
	for time.Now().Before(deadline) {
		p := f.state(t)
		if p.Run != nil && p.Run.Status == status {
			return p
		}
		if p.Run != nil && !loop.ActiveRunStatus(p.Run.Status) {
			t.Fatalf("unverified: waiting for %s reached %s", status, p.Run.Status)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("unverified: waiting for %s exceeded %s", status, timeout)
	return nil
}

func (f *labrigAcceptanceFixture) start(t *testing.T, id, prompt string) *loop.RunState {
	t.Helper()
	body := f.post(t, "commands", map[string]string{"command_id": id, "kind": "chat", "mode": "discussion", "text": prompt}, 202)
	var state loop.RunState
	if err := json.Unmarshal(body["run"], &state); err != nil || state.ID == "" {
		t.Fatal("command did not return a run identity")
	}
	f.evidence["run_id"], f.evidence["command_id"] = state.ID, id
	return &state
}

func (f *labrigAcceptanceFixture) checkArtifact(t *testing.T, p *loop.Projection, runID, name, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.ws, name))
	if err != nil || string(data) != want {
		t.Fatalf("unverified: artifact %s=%q err=%v", name, data, err)
	}
	calls, results := labrigToolCounts(p.Events)
	if p.Run == nil || p.Run.ID != runID || p.Run.Status != "completed" || p.Running || calls < 2 || results != calls {
		t.Fatalf("unverified: terminal run=%+v tool calls/results=%d/%d", p.Run, calls, results)
	}
	f.evidence["artifact"] = map[string]string{"path": name, "content": string(data)}
}

func TestLABRIGRealCorePauseResume(t *testing.T) {
	f := newLABRIGAcceptanceFixture(t, "controls/pause")
	st := f.start(t, "labrig-pause", "Use write to create pause.md containing exactly LABRIG_PAUSE_OK followed by a newline. Then use read to check pause.md. Reply LABRIG_PAUSE_OK only after the read confirms it. Do not create other files; no plan needed.")
	if n := f.probe.waitSent(t); n != 1 {
		t.Fatalf("unverified: first upstream request was %d", n)
	}
	p := f.state(t)
	if p.Run == nil || p.Run.ID != st.ID || p.Run.Phase != "model_call" {
		t.Fatalf("unverified: pause did not land during model_call: %+v", p.Run)
	}
	f.evidence["pause_observed_phase"] = p.Run.Phase
	f.post(t, "pause", loop.RunControl{RunID: st.ID, ID: "pause-1", Version: p.Run.ControlVersion}, 200)
	paused := f.waitStatus(t, "paused", 20*time.Second)
	beforeCalls, beforeResults := labrigToolCounts(paused.Events)
	beforeRequests := len(f.probe.observations())
	if paused.Run.ID != st.ID || beforeCalls != 0 {
		t.Fatal("unverified: pause lost owner or tools ran before the paused boundary")
	}
	f.post(t, "commands", map[string]string{"command_id": "paused-competing", "kind": "chat", "mode": "discussion", "text": "Reply PAUSED_COMPETITOR; do not use tools."}, http.StatusConflict)
	f.evidence["paused_competing_command_http"] = http.StatusConflict
	time.Sleep(600 * time.Millisecond)
	after := f.state(t)
	afterCalls, afterResults := labrigToolCounts(after.Events)
	if after.Run == nil || after.Run.ID != st.ID || after.Run.Status != "paused" || after.Run.Calls != paused.Run.Calls || beforeCalls != afterCalls || beforeResults != afterResults || len(f.probe.observations()) != beforeRequests {
		t.Fatal("unverified: paused owner continued model or tool activity")
	}
	if _, err := os.Stat(filepath.Join(f.ws, "pause.md")); !os.IsNotExist(err) {
		t.Fatal("unverified: paused run produced an artifact")
	}
	f.evidence["paused_dwell_ms"], f.evidence["paused_owner_retained"] = 600, true
	f.post(t, "resume", loop.RunControl{RunID: st.ID, ID: "resume-1", Version: after.Run.ControlVersion}, 200)
	completed := f.waitStatus(t, "completed", 120*time.Second)
	f.checkArtifact(t, completed, st.ID, "pause.md", "LABRIG_PAUSE_OK\n")
	if len(labrigControlReceipts(completed.Events)) != 2 {
		t.Fatal("unverified: pause/resume receipts missing")
	}
	f.verified = true
}

func TestLABRIGRealCoreSteer(t *testing.T) {
	f := newLABRIGAcceptanceFixture(t, "controls/steer")
	st := f.start(t, "labrig-steer", "Use write to create steer.md containing exactly LABRIG_ORIGINAL followed by a newline. Then read steer.md and confirm the contents. Do not create other files; no plan needed.")
	if n := f.probe.waitSent(t); n != 1 {
		t.Fatalf("unverified: first upstream request was %d", n)
	}
	p := f.state(t)
	if p.Run == nil || p.Run.ID != st.ID || p.Run.Phase != "model_call" {
		t.Fatalf("unverified: steer did not land during model_call: %+v", p.Run)
	}
	steering := "Change the required marker now: write steer.md containing exactly LABRIG_STEERED followed by a newline, replacing the original marker requirement. Then read steer.md. Reply LABRIG_STEERED only after the read confirms it. No other files."
	f.post(t, "steer", loop.RunControl{RunID: st.ID, ID: "steer-1", Version: p.Run.ControlVersion, Text: steering}, 200)
	if n := f.probe.waitSent(t); n != 2 {
		t.Fatalf("unverified: next upstream request was %d", n)
	}
	observations := f.probe.observations()
	delivered := false
	for _, msg := range observations[1].Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, steering) {
			delivered = true
		}
	}
	if !delivered {
		t.Fatal("unverified: next real model request omitted the accepted steering text")
	}
	f.evidence["steer_observed_phase"], f.evidence["steer_delivered_request"] = p.Run.Phase, 2
	f.evidence["steering_text"] = steering
	completed := f.waitStatus(t, "completed", 120*time.Second)
	f.checkArtifact(t, completed, st.ID, "steer.md", "LABRIG_STEERED\n")
	receipts := labrigControlReceipts(completed.Events)
	if len(receipts) != 1 || !strings.Contains(string(receipts[0]), `"steer-1"`) {
		t.Fatal("unverified: steering receipt missing")
	}
	f.verified = true
}

// Same real write tool, with a deterministic process exit after the effect but
// before the loop can append tool.result. This is an isolated test subprocess,
// not a Cerveau service or the inference Core.
type labrigCrashWrite struct {
	*tools.Write
	root string
}

func (w labrigCrashWrite) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var target struct{ Path, Content string }
	if json.Unmarshal(args, &target) != nil || target.Path != "recovery.md" || target.Content != "LABRIG_RECOVERY_ONCE\n" {
		return "", fmt.Errorf("write recovery.md with exactly LABRIG_RECOVERY_ONCE followed by a newline")
	}
	out, err := w.Write.Execute(ctx, args)
	if err != nil {
		return out, err
	}
	if err := labrigWriteEvidence(filepath.Join(w.root, "effect-receipt.json"), map[string]any{"path": target.Path, "content": target.Content, "effect_count": 1, "at": time.Now().UTC()}); err != nil {
		return "", err
	}
	os.Exit(93)
	return "", nil
}

func runLABRIGRecoveryChild(t *testing.T, root string) {
	t.Helper()
	sid, endpoint := os.Getenv("CERVEAU_ACCEPTANCE_RECOVERY_SID"), os.Getenv("CERVEAU_ACCEPTANCE_RECOVERY_PROXY")
	if sid == "" || endpoint == "" {
		t.Fatal("incomplete isolated recovery child configuration")
	}
	store, err := session.NewFSStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "workspace")
	a := New(config.Default(), store)
	reg := tools.NewRegistry(tools.Entry{Tool: labrigCrashWrite{tools.NewWrite(ws), root}, RiskTier: tools.RiskSafe})
	reg.SetWorkspace(ws)
	l := loop.New(llm.NewClient(endpoint), reg, a.Writer, store.EventsPath, nil)
	l.SetWorkspaceFunc(func(string) string { return ws })
	l.SetThinking("off", "low")
	l.SetSampling("strict")
	wr, err := a.Writer(sid)
	if err != nil {
		t.Fatal(err)
	}
	brief := "Use write to create recovery.md containing exactly LABRIG_RECOVERY_ONCE followed by a newline. Do not create other files."
	if _, err := wr.Append(episodic.MsgUser, map[string]string{"text": brief}); err != nil {
		t.Fatal(err)
	}
	committed := &loop.Plan{Title: "LABRIG isolated mid-step recovery", Steps: []loop.PlanStep{{
		Title: "Write the recovery marker", Detail: brief, Files: []string{"recovery.md"},
		Verify: &plan.Verify{Kind: "contains", File: "recovery.md", Symbol: "LABRIG_RECOVERY_ONCE"},
	}}}
	if _, err := wr.Append(episodic.Plan, committed); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	_, err = l.RunStep(ctx, sid, loop.StepRunRequest{Step: 0})
	if err != nil {
		t.Fatalf("real recovery child failed before its effect boundary: %v", err)
	}
	t.Fatal("real model did not execute the write needed for recovery acceptance")
}

func TestLABRIGRealCoreRecovery(t *testing.T) {
	if child := os.Getenv("CERVEAU_ACCEPTANCE_RECOVERY_CHILD"); child != "" {
		runLABRIGRecoveryChild(t, child)
		return
	}
	f := newLABRIGAcceptanceFixture(t, "recovery")
	ctx, cancel := context.WithDeadline(context.Background(), f.deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLABRIGRealCoreRecovery$", "-test.v")
	cmd.Env = append(os.Environ(), "CERVEAU_ACCEPTANCE_RECOVERY_CHILD="+f.root, "CERVEAU_ACCEPTANCE_RECOVERY_SID="+f.sid, "CERVEAU_ACCEPTANCE_RECOVERY_PROXY="+f.probe.server.URL)
	output, err := cmd.CombinedOutput()
	// Child diagnostics may contain upstream errors; redact the inherited key.
	if key := os.Getenv("CRV_MODEL_KEY"); key != "" {
		output = bytes.ReplaceAll(output, []byte(key), []byte("[redacted]"))
	}
	if saveErr := labrigWriteEvidence(filepath.Join(f.root, "child.json"), map[string]any{"diagnostic": string(output), "expected_exit": 93}); saveErr != nil {
		t.Fatal(saveErr)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 93 {
		t.Fatalf("unverified: recovery child did not exit at the effect boundary: %v; retained child.json", err)
	}
	f.evidence["child_exit"] = 93
	p := f.state(t)
	if p.Run == nil || p.Run.Status != "interrupted" || p.Running {
		t.Fatalf("unverified: reopened API hid interrupted owner: %+v", p.Run)
	}
	if p.Run.Step != 0 || p.Plan == nil || len(p.Plan.Steps) != 1 || p.Plan.Steps[0].Status != "pending" || p.Plan.Done {
		t.Fatalf("unverified: interrupted plan step was not recovered as pending/unverified: run=%+v plan=%+v", p.Run, p.Plan)
	}
	f.evidence["run_id"] = p.Run.ID
	f.evidence["recovered_plan"] = p.Plan
	calls, results := labrigToolCounts(p.Events)
	if calls != 1 || results != 0 {
		t.Fatalf("unverified: unfinished tool outcome was invented or setup differed: %d/%d", calls, results)
	}
	var actualCall llm.ToolCall
	for _, ev := range p.Events {
		if ev.Type != episodic.ToolCall {
			continue
		}
		var call struct {
			ID, Name string
			RawArgs  string `json:"raw_args"`
		}
		if json.Unmarshal(ev.Payload, &call) != nil {
			t.Fatal("unverified: unreadable real tool call")
		}
		actualCall = llm.ToolCall{ID: call.ID, Type: "function", Function: llm.FunctionCall{Name: call.Name, Arguments: call.RawArgs}}
	}
	if actualCall.ID == "" || actualCall.Function.Name != "write" {
		t.Fatal("unverified: missing actual model write identity")
	}
	repaired := window.RepairToolGroups([]window.Item{{Kind: "assistant", Msg: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{actualCall}}}})
	if len(repaired) != 2 || !strings.Contains(repaired[1].Msg.Content, "Outcome not recorded") || !strings.Contains(repaired[1].Msg.Content, "Inspect current state before repeating") {
		t.Fatal("unverified: incomplete tool context did not retain unknown outcome")
	}
	f.evidence["unknown_tool_outcome"], f.evidence["tool_call_id"] = repaired[1].Msg.Content, actualCall.ID
	before, err := os.ReadFile(filepath.Join(f.ws, "recovery.md"))
	if err != nil || string(before) != "LABRIG_RECOVERY_ONCE\n" {
		t.Fatalf("unverified: crash artifact=%q err=%v", before, err)
	}
	journalBefore, err := os.ReadFile(f.store.EventsPath(f.sid))
	if err != nil {
		t.Fatal(err)
	}
	requestsBefore := len(f.probe.observations())
	for i := 0; i < 3; i++ {
		observed := f.state(t)
		if observed.Run == nil || observed.Run.ID != p.Run.ID || observed.Run.Status != "interrupted" || observed.Running {
			t.Fatal("unverified: recovery observer changed owner/state")
		}
	}
	after, err := os.ReadFile(filepath.Join(f.ws, "recovery.md"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("unverified: observer changed effect")
	}
	journalAfter, err := os.ReadFile(f.store.EventsPath(f.sid))
	if err != nil || !bytes.Equal(journalBefore, journalAfter) || requestsBefore != len(f.probe.observations()) {
		t.Fatal("unverified: observer replay produced new journal/model activity")
	}
	sum := sha256.Sum256(before)
	f.evidence["observer_replays"], f.evidence["artifact_sha256"] = 3, hex.EncodeToString(sum[:])
	f.evidence["artifact"] = map[string]string{"path": "recovery.md", "content": string(before)}
	f.verified = true
}
