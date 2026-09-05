package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cerveau/internal/api"
	"cerveau/internal/config"
	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/loop"
	"cerveau/internal/plan"
	"cerveau/internal/rfx"
	"cerveau/internal/session"
	"cerveau/internal/tools"
)

type labrigUIArtifact struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type labrigUICase struct {
	SessionID   string             `json:"session_id"`
	Name        string             `json:"name"`
	Workspace   string             `json:"workspace"`
	PlanEventID string             `json:"plan_event_id"`
	Artifacts   []labrigUIArtifact `json:"artifacts"`
}

const labrigObsoletePlannerManifest = "rfx: 1\npack: planner\nversion: 1.0.0\nauthor: LABRIG isolated acceptance fixture\ndescription: Obsolete external fixture; must never replace the built-in Planner.\nui:\n  session: true\n  turn: true\n"

// Unlike the scripted preview, this fixture forwards inference to the
// operator's EXISTING local Core. It does not initialize an idle tracker,
// engine lifecycle manager, production session store or production config.
// No inference starts until a browser explicitly submits a production command.
//
// Set CERVEAU_ACCEPTANCE_UI=1, CERVEAU_ACCEPTANCE_MODEL_URL and an existing fresh
// absolute CERVEAU_ACCEPTANCE_ROOT. The ui child directory is created with
// exclusive ownership: an existing directory is an error, never overwritten.
// CRV_MODEL_KEY/CRV_MODEL_NAME must be inherited privately by the caller.
//
// go test ./internal/server -run '^TestLABRIGRealCoreUIPreview$' -count=1 -v -timeout 16m
// GET  http://127.0.0.1:17707/__labrig/info identifies seeded cases.
// POST http://127.0.0.1:17707/__labrig/stop ends only this preview. All evidence
// remains on disk, including interrupted or unsuccessful runs.
func TestLABRIGRealCoreUIPreview(t *testing.T) {
	if os.Getenv("CERVEAU_ACCEPTANCE_UI") != "1" {
		t.Skip("set CERVEAU_ACCEPTANCE_UI=1 for opt-in real-Core browser acceptance")
	}
	endpoint := strings.TrimRight(os.Getenv("CERVEAU_ACCEPTANCE_MODEL_URL"), "/")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		t.Fatal("CERVEAU_ACCEPTANCE_MODEL_URL must be an existing local HTTP Core endpoint without credentials/query")
	}
	parent := os.Getenv("CERVEAU_ACCEPTANCE_ROOT")
	if !filepath.IsAbs(parent) {
		t.Fatal("CERVEAU_ACCEPTANCE_ROOT must be an existing fresh absolute evidence directory")
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "ui")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatalf("refusing to reuse UI evidence directory: %v", err)
	}
	workspaces := filepath.Join(root, "workspaces")
	if err := os.Mkdir(workspaces, 0700); err != nil {
		t.Fatal(err)
	}
	defaultWS := filepath.Join(workspaces, "default")
	if err := os.Mkdir(defaultWS, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRV_CORES_JSON", filepath.Join(root, "cores.json"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))

	fsStore, err := session.NewFSStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	fsStore.SetWorkspace(defaultWS)
	// Browser-created sessions receive their own new directory, never one of
	// the seeded cases. Explicit workspace selection is out of scope here.
	store := labrigUIStore{FSStore: fsStore, workspaces: workspaces}
	cfg := &config.Config{
		Project: config.ProjectName, Addr: "127.0.0.1:17707", Workspace: defaultWS,
		SessionsDir: filepath.Join(root, "sessions"), ModelCtx: 32768,
		ThinkingMode: "off", ThinkingEffort: "low", Sampling: "strict",
		Endpoints: config.Endpoints{Model: endpoint},
	}
	a := api.New(cfg, store)
	a.SetConfigPath(filepath.Join(root, "config.json"))
	registryFor := func(ws string) *tools.Registry {
		canonical, err := filepath.EvalSymlinks(ws)
		if err != nil || !labrigUIDescendant(root, canonical) {
			return nil
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return nil
		}
		reg := tools.NewRegistry(
			tools.Entry{Tool: tools.NewRead(canonical), RiskTier: tools.RiskSafe},
			tools.Entry{Tool: tools.NewWrite(canonical), RiskTier: tools.RiskSafe},
		)
		reg.SetWorkspace(canonical)
		return reg
	}
	l := loop.New(llm.NewClient(endpoint), registryFor(defaultWS), a.Writer, store.EventsPath, nil)
	l.SetWorkspaceFunc(func(sid string) string {
		if meta, err := store.Get(sid); err == nil {
			return meta.Workspace
		}
		return filepath.Join(root, "invalid-session-workspace")
	})
	l.SetRegistryForWorkspace(registryFor)
	l.SetThinking("off", "low")
	l.SetSampling("strict")
	a.SetLoop(l)

	// This deliberately obsolete, clearly labelled copy belongs only to this
	// new fixture. Production ~/.crv/rfx is never opened or changed.
	packDir := filepath.Join(root, "rfx", "planner")
	if err := os.MkdirAll(filepath.Join(packDir, "ui"), 0700); err != nil {
		t.Fatal(err)
	}
	obsolete := map[string][]byte{
		"pack.yaml":     []byte(labrigObsoletePlannerManifest),
		"ui/panel.html": []byte("<!doctype html><title>OBSOLETE LABRIG FIXTURE</title><p>This external Planner must never be served.</p>\n"),
	}
	fixtureHashes := map[string]string{}
	for name, data := range obsolete {
		labrigUIWriteNew(t, filepath.Join(packDir, name), data)
		fixtureHashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	loader := rfx.NewLoader(filepath.Join(root, "rfx"), func(name string) bool { return name == "read" || name == "write" }, rfx.WithBuiltinPlanner())
	a.SetRfxLoader(loader)
	l.SetReflexes(loader)
	if len(loader.Errors()) != 0 {
		t.Fatalf("isolated RFX loader failed: %v", loader.Errors())
	}
	var builtin *rfx.Pack
	for _, p := range loader.Packs() {
		if p.Pack == "planner" {
			copy := p
			builtin = &copy
		}
	}
	if builtin == nil || builtin.Origin != "builtin" || len(builtin.IgnoredInstalled) != 1 || builtin.IgnoredInstalled[0].Version != "1.0.0" {
		t.Fatalf("built-in precedence fixture not established: %+v", builtin)
	}

	cases := map[string]labrigUICase{}
	for _, spec := range []struct {
		key, name string
		markers   []string
	}{
		{"whole", "LABRIG Whole Plan", []string{"WHOLE_ONE", "WHOLE_TWO"}},
		{"selected", "LABRIG Selected", []string{"SELECTED_ONE", "SELECTED_TWO", "UNSELECTED_THREE"}},
	} {
		ws := filepath.Join(workspaces, spec.key)
		if err := os.Mkdir(ws, 0700); err != nil {
			t.Fatal(err)
		}
		meta, err := fsStore.CreateInWorkspace(spec.name, ws)
		if err != nil {
			t.Fatal(err)
		}
		wr, err := a.Writer(meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wr.Append(episodic.MsgUser, map[string]string{"text": "LABRIG real-Core acceptance: execute only the requested scope of the seeded marker plan. Use write for each marker and read to confirm it. Do not edit a different step's file, create other files, or replace this plan. This is a harness-authored plan, not a model-generated benchmark."}); err != nil {
			t.Fatal(err)
		}
		p := &loop.Plan{Title: spec.name + " — seeded acceptance plan"}
		c := labrigUICase{SessionID: meta.ID, Name: meta.Name, Workspace: ws}
		for i, marker := range spec.markers {
			file := fmt.Sprintf("%s-%d.md", spec.key, i+1)
			content := marker + "\n"
			p.Steps = append(p.Steps, loop.PlanStep{
				Title: fmt.Sprintf("Write %s", marker), Files: []string{file}, Risk: "safe",
				Detail: fmt.Sprintf("Use write to create %s containing exactly %q. Use read to confirm that file, then stop. Do not touch any other file.", file, content),
				Verify: &plan.Verify{Kind: "contains", File: file, Symbol: marker},
			})
			c.Artifacts = append(c.Artifacts, labrigUIArtifact{Path: file, Content: content})
		}
		ev, err := wr.Append(episodic.Plan, p)
		if err != nil {
			t.Fatal(err)
		}
		c.PlanEventID = ev.ID
		cases[spec.key] = c
	}
	info := map[string]any{
		"fixture": "LABRIG real W8A16 acceptance", "root": parent, "ui_root": root, "model_url": endpoint,
		"cases": cases, "obsolete_planner_path": packDir, "obsolete_planner_sha256": fixtureHashes,
		"settings":    map[string]string{"thinking_mode": "off", "thinking_effort": "low", "sampling": "strict"},
		"plan_source": "harness-seeded", "started_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	labrigUIWriteNew(t, filepath.Join(root, "info.json"), append(encoded, '\n'))

	app := New(cfg.Addr, a)
	production := app.Handler
	stopped := make(chan struct{})
	var stopOnce sync.Once
	app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "cross-origin acceptance request rejected", http.StatusForbidden)
			return
		}
		switch {
		case r.URL.Path == "/__labrig/info" && r.Method == http.MethodGet:
			writeJSON(w, info)
		case r.URL.Path == "/__labrig/stop" && r.Method == http.MethodPost:
			writeJSON(w, map[string]string{"status": "stopping isolated acceptance server; evidence retained"})
			stopOnce.Do(func() { close(stopped) })
		case labrigUIAllowed(r.Method, r.URL.Path):
			production.ServeHTTP(w, r)
		default:
			http.Error(w, "action disabled in isolated acceptance; evidence and operator services are protected", http.StatusForbidden)
		}
	})
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	defer func() {
		for _, sid := range l.RunningSessions() {
			l.Kill(sid)
		}
		deadline := time.Now().Add(5 * time.Second)
		for len(l.RunningSessions()) > 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		l.WaitBackground(time.Second)
		metas, _ := store.List()
		for _, meta := range metas {
			if snapshot, err := l.Snapshot(meta.ID); err == nil {
				if data, err := json.MarshalIndent(snapshot, "", "  "); err == nil {
					labrigUIWriteNew(t, filepath.Join(root, "final-"+meta.ID+".json"), append(data, '\n'))
				}
			}
			if wr, err := a.Writer(meta.ID); err == nil {
				_ = wr.Close()
			}
		}
		unchanged := true
		for name, expected := range fixtureHashes {
			data, err := os.ReadFile(filepath.Join(packDir, name))
			if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != expected {
				unchanged = false
				t.Errorf("obsolete fixture changed: %s (read error: %v)", name, err)
			}
		}
		receipt, err := json.MarshalIndent(map[string]any{"path": packDir, "sha256": fixtureHashes, "unchanged": unchanged, "checked_at": time.Now().UTC().Format(time.RFC3339Nano)}, "", "  ")
		if err == nil {
			labrigUIWriteNew(t, filepath.Join(root, "obsolete-planner-check.json"), append(receipt, '\n'))
		}
		t.Logf("LABRIG_REAL_CORE_UI_RETAINED %s; obsolete Planner copy unchanged=%v", root, unchanged)
	}()
	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(listener) }()
	t.Logf("LABRIG_REAL_CORE_UI_READY http://%s/ evidence=%s", cfg.Addr, root)
	select {
	case <-stopped:
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	case <-time.After(15 * time.Minute):
		t.Error("real-Core UI acceptance expired; unfinished cases remain unverified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = app.Shutdown(ctx)
}

// Restrict the preview to observation, creation of isolated sessions, and the
// actual production command/control APIs under test. No delete, rewind, event
// injection, config writes, pairing, installed RFX edits, or engine operations.
func labrigUIAllowed(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead {
		if !strings.HasPrefix(path, "/api/") {
			return !strings.HasPrefix(path, "/__labrig/") && path != "/pair" && !strings.HasPrefix(path, "/p/")
		}
		switch path {
		case "/api/health", "/api/build", "/api/sampling", "/api/thinking", "/api/cores", "/api/idle", "/api/sessions", "/api/skills", "/api/rfx", "/api/system/stats":
			return true
		}
		return strings.HasPrefix(path, "/api/sessions/") || strings.HasPrefix(path, "/api/rfx/panel/")
	}
	if method != http.MethodPost {
		return false
	}
	if path == "/api/sessions" || path == "/api/files/probe" {
		return true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "sessions" || parts[2] == "" {
		return false
	}
	switch parts[3] {
	case "commands", "pause", "resume", "steer", "kill", "answer":
		return true
	}
	return false
}

type labrigUIStore struct {
	*session.FSStore
	workspaces string
}

func (s labrigUIStore) CreateInWorkspace(name, workspace string) (*session.Meta, error) {
	if workspace != "" {
		return nil, fmt.Errorf("explicit workspace selection is disabled in isolated acceptance")
	}
	ws, err := os.MkdirTemp(s.workspaces, "browser-session-")
	if err != nil {
		return nil, err
	}
	// FSStore IDs include a second-resolution timestamp and name. Give fixture
	// creations a unique suffix so rapid same-name requests cannot overwrite a
	// previous evidence journal. Seeded cases keep their exact display names.
	return s.FSStore.CreateInWorkspace(name+" "+filepath.Base(ws), ws)
}

func labrigUIDescendant(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func labrigUIWriteNew(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}

func TestLABRIGRealCoreUIRouteBoundary(t *testing.T) {
	p, err := rfx.ParsePack([]byte(labrigObsoletePlannerManifest), "fixture:planner/pack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := rfx.ValidatePack(p); err != nil {
		t.Fatal(err)
	}
	if p.Version != "1.0.0" {
		t.Fatal("obsolete fixture version must be readable for UI disclosure")
	}
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/", true}, {"GET", "/api/rfx/panel/planner", true},
		{"GET", "/api/sessions/fixture/state", true}, {"POST", "/api/sessions/fixture/commands", true},
		{"POST", "/api/sessions/fixture/pause", true}, {"POST", "/api/sessions/fixture/resume", true},
		{"POST", "/api/sessions/fixture/steer", true}, {"POST", "/api/sessions/fixture/kill", true},
		{"POST", "/api/sessions", true}, {"POST", "/api/files/probe", true},
		{"POST", "/api/cores/select", false}, {"POST", "/api/cores/apply", false},
		{"GET", "/api/cores/operator/params", false}, {"GET", "/api/fs/list", false},
		{"POST", "/api/idle/now", false}, {"POST", "/api/config/workspace", false},
		{"POST", "/api/sessions/fixture/events", false}, {"POST", "/api/sessions/fixture/rewind", false},
		{"DELETE", "/api/sessions/fixture", false}, {"POST", "/api/rfx/toggle", false},
		{"POST", "/api/sessions/instant", false},
		{"POST", "/api/pair", false}, {"GET", "/pair", false}, {"GET", "/p/device", false},
	} {
		if got := labrigUIAllowed(tc.method, tc.path); got != tc.allowed {
			t.Errorf("%s %s allowed=%v, want %v", tc.method, tc.path, got, tc.allowed)
		}
	}
}
