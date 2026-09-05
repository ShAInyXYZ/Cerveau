package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cerveau/internal/config"
	"cerveau/internal/llm"
	"cerveau/internal/loop"
	"cerveau/internal/session"
	"cerveau/internal/tools"
)

// Explicit opt-in only. Uses the already-running Core, isolated temporary
// sessions/files and the real command API/dispatcher. Never starts, switches,
// reconfigures or parks an inference engine or touches production sessions.
func TestLABRIGRealModelToolSmoke(t *testing.T) {
	endpoint := os.Getenv("CERVEAU_SMOKE_MODEL_URL")
	if endpoint == "" {
		t.Skip("set CERVEAU_SMOKE_MODEL_URL for the opt-in real-Core smoke")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "workspace")
	if err := os.Mkdir(ws, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewFSStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.CreateInWorkspace("LABRIG isolated smoke", ws)
	if err != nil {
		t.Fatal(err)
	}
	a := New(config.Default(), store)
	reg := tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSafe},
	)
	l := loop.New(llm.NewClient(endpoint), reg, a.Writer, store.EventsPath, nil)
	l.SetWorkspaceFunc(func(string) string { return ws })
	l.SetThinking("off", "low")
	l.SetSampling("strict")
	a.SetLoop(l)
	defer func() {
		l.Kill(meta.ID)
		deadline := time.Now().Add(3 * time.Second)
		for len(l.RunningSessions()) > 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		l.WaitBackground(time.Second)
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/commands", a.Command)
	mux.HandleFunc("GET /api/sessions/{id}/state", a.SessionState)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	body, _ := json.Marshal(map[string]any{"command_id": "labrig-smoke", "kind": "chat", "mode": "discussion", "text": "Small harness smoke test. Use the write tool to create labrig-smoke.md containing exactly LABRIG_OK followed by a newline. Then use read to check that file. Reply LABRIG_OK only after the read confirms its contents. Do not create any other file; no plan needed."})
	resp, err := http.Post(srv.URL+"/api/sessions/"+meta.ID+"/commands", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var accepted struct{ Run loop.RunState }
	json.NewDecoder(resp.Body).Decode(&accepted)
	resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("command rejected: %d", resp.StatusCode)
	}
	deadline := time.Now().Add(80 * time.Second)
	for time.Now().Before(deadline) {
		p, err := l.Snapshot(meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.Run != nil && !p.Running {
			data, readErr := os.ReadFile(filepath.Join(ws, "labrig-smoke.md"))
			calls, results := 0, 0
			for _, ev := range p.Events {
				if ev.Type == "tool.call" {
					calls++
				}
				if ev.Type == "tool.result" {
					results++
				}
			}
			if p.Run.Status != "completed" || readErr != nil || string(data) != "LABRIG_OK\n" || calls < 2 || results != calls {
				t.Fatalf("smoke failed: state=%+v artifact=%q err=%v tool calls/results=%d/%d", p.Run, data, readErr, calls, results)
			}
			t.Logf("real Core smoke passed: run=%s calls=%d tool_pairs=%d artifact=LABRIG_OK", p.Run.ID, p.Run.Calls, calls)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("real Core smoke exceeded 80 seconds; run cancelled by cleanup")
}
