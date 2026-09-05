package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cerveau/internal/api"
	"cerveau/internal/config"
	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/loop"
	"cerveau/internal/plan"
	"cerveau/internal/session"
	"cerveau/internal/tools"
)

// TestLABRIGUIPreview serves the real production router and embedded panel for
// opt-in browser QA. Only downstream model/health services are scripted. It does
// not contact the operator's Core, memory services, sessions or configuration.
//
// First rebuild panel assets, then run:
// CERVEAU_UI_PREVIEW=1 go test ./internal/server -run '^TestLABRIGUIPreview$' -count=1 -v -timeout 6m
// POST http://127.0.0.1:17706/__labrig/start starts another 30-second run.
// POST http://127.0.0.1:17706/__labrig/stop ends the preview and removes its temp data.
func TestLABRIGUIPreview(t *testing.T) {
	if os.Getenv("CERVEAU_UI_PREVIEW") != "1" {
		t.Skip("set CERVEAU_UI_PREVIEW=1 for the opt-in embedded-panel preview")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "LABRIG-scripted-preview")
	if err := os.Mkdir(ws, 0700); err != nil {
		t.Fatal(err)
	}
	// This supported override keeps even Core-registry reads inside the fixture.
	t.Setenv("CRV_CORES_JSON", filepath.Join(root, "cores.json"))
	// Environment credentials are irrelevant to the scripted service and must
	// never be copied into fixture request logs or displayed in screenshots.
	t.Setenv("CRV_MODEL_KEY", "")
	const fixtureName = "LABRIG scripted preview (no real Core)"
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "fixture": fixtureName})
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": fixtureName}}})
		case "/props":
			_ = json.NewEncoder(w).Encode(map[string]any{"modalities": map[string]bool{"text": true}, "default_generation_settings": map[string]int{"n_ctx": 32768}})
		case "/debug":
			_ = json.NewEncoder(w).Encode(map[string]string{"version": "scripted-preview"})
		case "/v1/chat/completions":
			_, _ = io.Copy(io.Discard, r.Body)
			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "Scripted UI preview completed. This exercised the real API and run lifecycle, not a real model benchmark."}, "finish_reason": "stop"}},
				"usage":   map[string]int{"prompt_tokens": 120, "completion_tokens": 24},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer model.Close()
	store, err := session.NewFSStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	store.SetWorkspace(ws)
	meta, err := store.CreateInWorkspace("LABRIG scripted UI preview", ws)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Project: config.ProjectName, Addr: "127.0.0.1:17706", Workspace: ws,
		SessionsDir: filepath.Join(root, "sessions"), ModelCtx: 32768,
		ThinkingMode: "autopilot", ThinkingEffort: "medium", Sampling: "strict",
		Endpoints: config.Endpoints{Model: model.URL, Embedder: model.URL, Typesense: model.URL},
	}
	a := api.New(cfg, store)
	a.SetConfigPath(filepath.Join(root, "config.json"))
	reg := tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSafe},
	)
	l := loop.New(llm.NewClient(model.URL), reg, a.Writer, store.EventsPath, nil)
	l.SetWorkspaceFunc(func(string) string { return ws })
	l.SetThinking("autopilot", "medium")
	l.SetSampling("strict")
	a.SetLoop(l)
	defer func() {
		for _, id := range l.RunningSessions() {
			l.Kill(id)
		}
		deadline := time.Now().Add(3 * time.Second)
		for len(l.RunningSessions()) > 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		l.WaitBackground(time.Second)
		metas, _ := store.List()
		for _, m := range metas {
			if wr, err := a.Writer(m.ID); err == nil {
				_ = wr.Close()
			}
		}
	}()
	wr, err := a.Writer(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wr.Append(episodic.Note, map[string]string{"kind": "preview_fixture", "text": fixtureName + "; temporary session, real HTTP API and embedded panel"}); err != nil {
		t.Fatal(err)
	}
	if _, err := wr.Append(episodic.Plan, &loop.Plan{Title: "LABRIG preview — pending checks, not benchmark results", Steps: []loop.PlanStep{
		{Title: "Create the preview marker", Files: []string{"preview.md"}, Verify: &plan.Verify{Kind: "contains", File: "preview.md", Symbol: "LABRIG_PREVIEW"}},
		{Title: "Confirm the second marker", Files: []string{"confirmation.md"}, Verify: &plan.Verify{Kind: "contains", File: "confirmation.md", Symbol: "CONFIRMED"}},
	}}); err != nil {
		t.Fatal(err)
	}
	var sequence atomic.Uint64
	start := func() (*loop.RunState, error) {
		return l.Start(meta.ID, loop.Command{
			ID: fmt.Sprintf("ui-preview-%d", sequence.Add(1)), Kind: "chat", Mode: "discussion",
			Text: "Scripted UI preview: hold this model call so I can inspect pause, resume, stop and reload. The seeded plan is display data with pending checks; do not claim it passed.",
		}, nil)
	}
	initial, err := start()
	if err != nil {
		t.Fatal(err)
	}
	app := New(cfg.Addr, a)
	production := app.Handler
	stopped := make(chan struct{})
	var stopOnce sync.Once
	app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The test endpoints cannot be driven cross-origin by another website.
		if strings.HasPrefix(r.URL.Path, "/__labrig/") {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
				http.Error(w, "cross-origin preview command rejected", http.StatusForbidden)
				return
			}
			switch r.URL.Path {
			case "/__labrig/info":
				writeJSON(w, map[string]any{"fixture": fixtureName, "session_id": meta.ID, "run": l.RunStateOf(meta.ID), "model_delay_seconds": 30, "workspace": ws})
			case "/__labrig/start":
				if r.Method != http.MethodPost {
					http.Error(w, "POST required", http.StatusMethodNotAllowed)
					return
				}
				run, err := start()
				if err != nil {
					w.WriteHeader(http.StatusConflict)
					writeJSON(w, map[string]any{"error": err.Error()})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				writeJSON(w, map[string]any{"session_id": meta.ID, "run": run, "fixture": fixtureName})
			case "/__labrig/stop":
				if r.Method != http.MethodPost {
					http.Error(w, "POST required", http.StatusMethodNotAllowed)
					return
				}
				writeJSON(w, map[string]string{"status": "stopping preview"})
				stopOnce.Do(func() { close(stopped) })
			default:
				http.NotFound(w, r)
			}
			return
		}
		// These production handlers otherwise use operator-level paths or can
		// switch an engine. Refuse them explicitly instead of faking responses.
		if strings.HasPrefix(r.URL.Path, "/api/cores/") || strings.HasPrefix(r.URL.Path, "/api/devices") || strings.HasPrefix(r.URL.Path, "/api/pair") || r.URL.Path == "/pair" || strings.HasPrefix(r.URL.Path, "/p/") || strings.HasPrefix(r.URL.Path, "/api/fs/") {
			http.Error(w, "operator-level action disabled in temporary UI preview", http.StatusForbidden)
			return
		}
		production.ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(listener) }()
	t.Logf("LABRIG_UI_PREVIEW_READY http://%s/ session=%s run=%s — %s", cfg.Addr, meta.ID, initial.ID, fixtureName)
	select {
	case <-stopped:
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Minute):
		t.Log("preview expired after five minutes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = app.Shutdown(ctx)
}
