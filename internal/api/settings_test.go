package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cerveau/internal/config"
	"cerveau/internal/llm"
	"cerveau/internal/loop"
)

func settingsFixture(t *testing.T) (*API, string) {
	t.Helper()
	cfg := config.Default()
	cfg.Sampling = "strict"
	a := New(cfg, nil)
	a.SetLoop(loop.New(llm.NewClient("http://unused.invalid"), nil, nil, nil, nil))
	a.chat.SetThinking(cfg.ThinkingMode, cfg.ThinkingEffort)
	a.chat.SetSampling(cfg.Sampling)
	path := filepath.Join(t.TempDir(), "config.json")
	a.SetConfigPath(path)
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	return a, path
}

func settingsRequest(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(body)))
	return rec
}

func readSettingsConfig(t *testing.T, path string) config.Config {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("invalid saved config: %v", err)
	}
	return cfg
}

func TestConcurrentDefaultsAndPairingPersistWithoutLostUpdates(t *testing.T) {
	a, path := settingsFixture(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errors := make(chan string, 64)
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if r := settingsRequest(a.SetThinking, `{"mode":"always","effort":"xhigh"}`); r.Code != 200 {
				errors <- r.Body.String()
			}
			_ = a.ConfigSnapshot()
		}()
		go func() {
			defer wg.Done()
			<-start
			if r := settingsRequest(a.SetSampling, `{"name":"neutral"}`); r.Code != 200 {
				errors <- r.Body.String()
			}
			_ = a.RemoteToken()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		if err := a.SetRemoteToken("fixture-token"); err != nil {
			errors <- err.Error()
		}
	}()
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	saved, live := readSettingsConfig(t, path), a.ConfigSnapshot()
	if saved != live {
		t.Fatalf("saved and accepted config differ: saved=%+v live=%+v", saved, live)
	}
	if saved.ThinkingMode != "always" || saved.ThinkingEffort != "xhigh" || saved.Sampling != "neutral" || saved.RemoteAccessToken != "fixture-token" {
		t.Fatalf("lost concurrent update: %+v", saved)
	}
	mode, effort := a.chat.Thinking()
	if mode != saved.ThinkingMode || effort != saved.ThinkingEffort || a.chat.SamplingName() != saved.Sampling {
		t.Fatalf("runtime defaults diverged from saved settings: %s/%s/%s", mode, effort, a.chat.SamplingName())
	}
}

func TestFailedDefaultsSaveDoesNotChangeRuntimeOrConfig(t *testing.T) {
	a, path := settingsFixture(t)
	before := a.ConfigSnapshot()
	// A directory cannot be overwritten as a config file. Only temp paths are
	// involved; this fixture does not touch the user's settings or model.
	a.SetConfigPath(t.TempDir())
	for _, request := range []struct {
		handler http.HandlerFunc
		body    string
	}{
		{a.SetThinking, `{"mode":"always","effort":"xhigh"}`},
		{a.SetSampling, `{"name":"creative"}`},
	} {
		if r := settingsRequest(request.handler, request.body); r.Code != 500 {
			t.Fatalf("save failure returned %d: %s", r.Code, r.Body.String())
		}
	}
	if err := a.SetRemoteToken("must-not-publish"); err == nil {
		t.Fatal("pairing save unexpectedly succeeded")
	}
	if a.ConfigSnapshot() != before || readSettingsConfig(t, path) != before {
		t.Fatal("failed save published config changes")
	}
	mode, effort := a.chat.Thinking()
	if mode != before.ThinkingMode || effort != before.ThinkingEffort || a.chat.SamplingName() != before.Sampling {
		t.Fatal("failed save retuned runtime defaults")
	}
}

func TestWorkspaceConfigSaveSerializesWithDefaults(t *testing.T) {
	a, path := settingsFixture(t)
	a.SetWorkspaceChanger(func(ws string) error {
		a.cfg.Workspace = ws // same legacy callback shape as main.go
		return config.Save(path, a.cfg)
	})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if r := settingsRequest(a.ChangeWorkspace, `{"path":"fixture-workspace"}`); r.Code != 200 {
			t.Errorf("workspace failed: %s", r.Body.String())
		}
	}()
	go func() {
		defer wg.Done()
		if r := settingsRequest(a.SetSampling, `{"name":"creative"}`); r.Code != 200 {
			t.Errorf("sampling failed: %s", r.Body.String())
		}
	}()
	wg.Wait()
	saved := readSettingsConfig(t, path)
	if saved.Workspace != "fixture-workspace" || saved.Sampling != "creative" {
		t.Fatalf("workspace save erased settings: %+v", saved)
	}
}

func TestAcceptedRunKeepsDefaultsWhileFutureRunsReceiveUpdates(t *testing.T) {
	entered, release := make(chan struct{}, 4), make(chan struct{})
	var once sync.Once
	releaseModel := func() { once.Do(func() { close(release) }) }
	f := newRunAPIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]string{"role": "assistant", "content": "fixture complete"}, "finish_reason": "stop",
		}}})
	})
	defer releaseModel()
	f.a.SetConfigPath(filepath.Join(t.TempDir(), "config.json"))
	f.l.SetThinking("plan", "low")
	f.l.SetSampling("strict")
	accepted := f.start(t, "before-defaults")
	waitModel(t, entered)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if r := settingsRequest(f.a.SetThinking, `{"mode":"always","effort":"xhigh"}`); r.Code != 200 {
			t.Errorf("thinking: %s", r.Body.String())
		}
	}()
	go func() {
		defer wg.Done()
		if r := settingsRequest(f.a.SetSampling, `{"name":"neutral"}`); r.Code != 200 {
			t.Errorf("sampling: %s", r.Body.String())
		}
	}()
	wg.Wait()
	active := f.l.RunStateOf(f.sid)
	if active.ID != accepted.ID || active.ThinkingMode != "plan" || active.ThinkingEffort != "low" || active.Sampling != "strict" {
		t.Fatalf("accepted run was retuned: %+v", active)
	}
	releaseModel()
	f.wait(t, "completed")
	next := f.start(t, "after-defaults")
	if next.ThinkingMode != "always" || next.ThinkingEffort != "xhigh" || next.Sampling != "neutral" {
		t.Fatalf("future run missed acknowledged defaults: %+v", next)
	}
	f.wait(t, "completed")
}
