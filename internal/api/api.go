package api

import (
	"cerveau/internal/llm"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cerveau/internal/codeintel"
	"cerveau/internal/config"
	"cerveau/internal/episodic"
	"cerveau/internal/idle"
	"cerveau/internal/loop"
	"cerveau/internal/memory"
	"cerveau/internal/rfx"
	"cerveau/internal/session"
	"cerveau/internal/skills"
	"cerveau/internal/tools"
)

// Version is the running app version, surfaced in /health.
const Version = "0.6.0-alpha"

var BuildRevision = "development"

type API struct {
	cfgMu         sync.RWMutex
	cfg           *config.Config
	configPath    string
	sess          session.Store
	http          *http.Client
	chat          *loop.Loop
	runningFn     func() []string // tests only; see runningSessions
	sctx          *tools.SessionContext
	ci            *codeintel.Indexer
	mem           *memory.TSClient
	started       time.Time
	idle          *idle.Tracker
	idleObserveMu sync.Mutex
	idleCoreState func(context.Context) idle.State

	wmu     sync.Mutex
	writers map[string]*episodic.Writer

	qmu         sync.Mutex
	questions   map[string]*pendingQuestion
	skillLoader *skills.Loader
	rfxLoader   *rfx.Loader
	wsChange    func(string) error
	// modelCtx reports the window the packer is actually using — the Core's
	// own once it has answered, the configured number until then.
	modelCtx func() int
	ctxSync  func(context.Context)
}

type pendingQuestion struct {
	ID       string   `json:"id"`
	RunID    string   `json:"run_id"`
	Answered bool     `json:"-"`
	Question string   `json:"question"`
	Options  []string `json:"options"`
	ch       chan string
}

func New(cfg *config.Config, sess session.Store) *API {
	return &API{
		cfg:       cfg,
		sess:      sess,
		http:      &http.Client{Timeout: 2 * time.Second, Transport: llm.CoreTransport()},
		writers:   map[string]*episodic.Writer{},
		questions: map[string]*pendingQuestion{},
		started:   time.Now(),
	}
}

// SetConfigPath lets SetRemoteToken persist pairing to disk.
func (a *API) SetConfigPath(p string) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.configPath = p
}

func (a *API) SetLoop(l *loop.Loop) { a.chat = l }

// RemoteToken is the gate's bearer secret (empty = unpaired localhost mode).
func (a *API) RemoteToken() string { return a.ConfigSnapshot().RemoteAccessToken }

// SetRemoteToken persists a freshly minted pairing token into the config.
func (a *API) SetRemoteToken(token string) error {
	return a.updateConfig(func(next *config.Config) { next.RemoteAccessToken = token }, nil, true)
}

func (a *API) SetSessionContext(sctx *tools.SessionContext) { a.sctx = sctx }

func (a *API) SetCodeIntel(ci *codeintel.Indexer) { a.ci = ci }

func (a *API) SetSkillLoader(l *skills.Loader) { a.skillLoader = l }

func (a *API) SetRfxLoader(l *rfx.Loader) { a.rfxLoader = l }

func (a *API) SetWorkspaceChanger(f func(string) error) { a.wsChange = f }

// PickWorkspace opens the OS native folder dialog (crv runs locally, so it can),
// then applies the chosen directory as the workspace. Browser sandboxes can't do
// this — the server does it and returns the picked path.
func (a *API) PickWorkspace(w http.ResponseWriter, r *http.Request) {
	if a.wsChange == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace change not wired"})
		return
	}
	path, err := pickDirectory(a.ConfigSnapshot().Workspace)
	if err != nil {
		// user cancelled or no picker available — not an error worth alarming on
		writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
		return
	}
	workspace, err := a.changeWorkspace(path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"workspace": workspace})
}

func (a *API) ChangeWorkspace(w http.ResponseWriter, r *http.Request) {
	if a.wsChange == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace change not wired"})
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	workspace, err := a.changeWorkspace(body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "workspace": workspace})
}

func (a *API) ListSkills(w http.ResponseWriter, r *http.Request) {
	if a.skillLoader == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []any{}})
		return
	}
	type skillView struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Triggers    []string `json:"triggers"`
		Tools       []string `json:"tools"`
	}
	out := []skillView{}
	for _, s := range a.skillLoader.List() {
		toolNames := []string{}
		for _, t := range s.Tools {
			toolNames = append(toolNames, t.Name)
		}
		out = append(out, skillView{Name: s.Name, Description: s.Description, Triggers: s.Triggers, Tools: toolNames})
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

func (a *API) ReindexCode(w http.ResponseWriter, r *http.Request) {
	if a.ci == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "code intel not wired"})
		return
	}
	rep, err := a.ci.Index(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

type ComponentStatus struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Info   string `json:"info,omitempty"` // model name / version — the "what"
}

func (a *API) Health(w http.ResponseWriter, r *http.Request) {
	cfg := a.ConfigSnapshot()
	state := a.RefreshIdle(r.Context())
	model := ComponentStatus{Name: "model", URL: cfg.Endpoints.Model, Detail: "Core is " + string(state)}
	if a.idle != nil && state == "" {
		model.Detail = "Core state unknown; model health probe skipped"
	}
	// Wired idle tracking requires fresh confirmation that the service is
	// active. Its remembered display state must not wake a parked socket when
	// current metadata is unavailable. Unwired deployments retain HTTP probes.
	if a.idle == nil || state == idle.Active {
		model = a.ping("model", cfg.Endpoints.Model, "/health")
	}
	if model.OK {
		model.Info = a.probeModelName(cfg.Endpoints.Model)
		if a.ctxSync != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			a.ctxSync(ctx)
			cancel()
		}
	}
	embedder := a.ping("embedder", cfg.Endpoints.Embedder, "/health")
	if embedder.OK {
		embedder.Info = a.probeModelName(cfg.Endpoints.Embedder)
	}
	ts := a.ping("typesense", cfg.Endpoints.Typesense, "/health")
	if ts.OK {
		ts.Info = a.probeTypesenseVersion(cfg.Endpoints.Typesense)
	}

	ws := cfg.Workspace
	if abs, err := filepath.Abs(ws); err == nil {
		ws = abs
	}
	up := ""
	if !a.started.IsZero() {
		up = time.Since(a.started).Round(time.Second).String()
	}
	// Only successful /props reports establish non-text capabilities. Missing
	// keys mean unknown, not unsupported; model names are not live evidence.
	var modalities map[string]bool
	if model.OK {
		modalities = a.probeModalities(cfg.Endpoints.Model)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"components": []ComponentStatus{model, embedder, ts},
		"workspace":  ws,
		"modes":      []string{"discussion", "brainstorming", "autopilot"},
		"model": map[string]any{
			"name":       model.Info,
			"modalities": modalities, // absent vision/audio/video = unconfirmed
		},
		"system": map[string]any{
			"version":   Version,
			"model_ctx": a.contextWindow(),
			"uptime":    up,
			"typesense": map[string]any{"managed": cfg.TypesenseManaged},
			"sessions":  cfg.SessionsDir,
		},
	})
}

// coreAuth adds the Core's bearer token (CRV_MODEL_KEY, the same one the LLM
// client sends) to a probe. A vLLM Core started with VLLM_API_KEY answers 401
// to /v1/models without it — which read as "model name unknown" in health and,
// worse, left the window at its config fallback instead of the Core's 262k.
func coreAuth(req *http.Request) {
	if k := strings.TrimSpace(os.Getenv("CRV_MODEL_KEY")); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
}

// probeModalities preserves true/false only when the running server explicitly
// reports a boolean in a successful /props response. Missing, null, malformed,
// unsupported endpoints and transport errors leave capability UNKNOWN (absent).
// A configured profile or model name does not establish live vision support.
func (a *API) probeModalities(base string) map[string]bool {
	out := map[string]bool{"text": true}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/props", nil)
	if err != nil {
		return out
	}
	coreAuth(req)
	resp, err := a.http.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return out
	}
	var props struct {
		Modalities map[string]json.RawMessage `json:"modalities"`
	}
	if json.Unmarshal(raw, &props) != nil {
		return out
	}
	for _, key := range []string{"vision", "audio", "video"} {
		value := strings.TrimSpace(string(props.Modalities[key]))
		if value == "true" {
			out[key] = true
		} else if value == "false" {
			out[key] = false
		}
	}
	return out
}

// probeModelName hits /v1/models and returns a cleaned model id.
func (a *API) probeModelName(base string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	coreAuth(req)
	resp, err := a.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.Data) == 0 {
		return ""
	}
	id := out.Data[0].ID
	// strip path + .gguf extension -> just the model name
	if i := strings.LastIndexAny(id, "/\\"); i >= 0 {
		id = id[i+1:]
	}
	id = strings.TrimSuffix(id, ".gguf")
	return id
}

func (a *API) probeTypesenseVersion(base string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/debug", nil)
	req.Header.Set("X-TYPESENSE-API-KEY", a.ConfigSnapshot().TypesenseKey)
	resp, err := a.http.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Version string `json:"version"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	if out.Version != "" {
		return "v" + out.Version
	}
	return ""
}

func (a *API) ping(name, base, path string) ComponentStatus {
	st := ComponentStatus{Name: name, URL: base}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		st.Detail = err.Error()
		return st
	}
	coreAuth(req)
	resp, err := a.http.Do(req)
	if err != nil {
		st.Detail = "unreachable"
		return st
	}
	resp.Body.Close()
	st.OK = resp.StatusCode < 500
	if !st.OK {
		st.Detail = resp.Status
	}
	return st
}

func (a *API) ListSessions(w http.ResponseWriter, r *http.Request) {
	metas, err := a.sess.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Which sessions have a turn executing RIGHT NOW. Without this the panel
	// can only know about turns it started itself, so a CLI build is
	// indistinguishable from an idle session — the user watches a still screen
	// while the machine works.
	running := []string{}
	if a.chat != nil {
		running = a.chat.RunningSessions()
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": metas, "running": running})
}

func (a *API) CreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		Workspace string `json:"workspace,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	m, err := a.sess.CreateInWorkspace(body.Name, body.Workspace)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// CreateInstant makes an ephemeral scratch session — no memory, auto-deleted.
func (a *API) CreateInstant(w http.ResponseWriter, r *http.Request) {
	m, err := a.sess.CreateInstant()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// RenameSession changes a session's display name only — the id stays fixed.
func (a *API) RenameSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	m, err := a.sess.Rename(id, strings.TrimSpace(body.Name))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// confirmCode derives a stable 4-char code from the session id — the user must
// type "<name>#<code>" to confirm a delete. Deterministic so the server needs no
// per-request state; it's an anti-fat-finger guard, not a secret.
func confirmCode(id string) string {
	sum := sha1.Sum([]byte(id))
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no ambiguous 0/O/1/I
	out := make([]byte, 4)
	for i := 0; i < 4; i++ {
		out[i] = alphabet[int(sum[i])%len(alphabet)]
	}
	return string(out)
}

// DeletePreview returns the blast radius (what will be removed) + the confirm code.
func (a *API) DeletePreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var name string
	for _, m := range mustList(a) {
		if m.ID == id {
			name = m.Name
		}
	}
	if name == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	episodic, semantic := 0, 0
	if a.mem != nil {
		if hits, err := a.mem.Search(r.Context(), "*", "episodic", id, 250, false, ""); err == nil {
			episodic = len(hits)
		}
		if hits, err := a.mem.Search(r.Context(), "*", "semantic", id, 250, false, ""); err == nil {
			semantic = len(hits)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       id,
		"name":     name,
		"events":   a.sess.CountEvents(id),
		"episodic": episodic,
		"semantic": semantic,
		"code":     confirmCode(id),
	})
}

// DeleteSession removes a session. mode: "session" (folder + events + episodic,
// KEEPS semantic summaries) | "all" (everything). NEVER touches the project
// workspace or user files. Requires the typed confirmation to match "<name>#<code>".
func (a *API) DeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var name string
	for _, m := range mustList(a) {
		if m.ID == id {
			name = m.Name
		}
	}
	if name == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	var body struct {
		Mode    string `json:"mode"`    // "session" | "all"
		Confirm string `json:"confirm"` // must equal "<name>#<code>"
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Confirm != name+"#"+confirmCode(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "confirmation text does not match"})
		return
	}

	// memories first (so a folder-delete failure doesn't orphan a half-clean state)
	epi, sem := 0, 0
	if a.mem != nil {
		epi, _ = a.mem.DeleteBySession(r.Context(), id, "episodic")
		if body.Mode == "all" {
			sem, _ = a.mem.DeleteBySession(r.Context(), id, "semantic")
		}
	}
	if err := a.sess.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":         true,
		"episodicRemoved": epi,
		"semanticRemoved": sem,
		"keptSemantic":    body.Mode != "all",
	})
}

func mustList(a *API) []session.Meta {
	m, _ := a.sess.List()
	return m
}

func (a *API) SessionEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := os.Open(a.sess.EventsPath(id))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	io.Copy(w, f)
}

func (a *API) AppendEvent(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	id := r.PathValue("id")
	var body struct {
		Type    episodic.EventType `json:"type"`
		Payload json.RawMessage    `json:"payload"`
	}
	if !decodeBoundedCommand(w, r, &body) {
		return
	}
	if body.Type == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type and payload required"})
		return
	}
	if body.Type == episodic.MsgUser {
		normalized, err := normalizeUserEventImages(r.Context(), body.Payload)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		body.Payload = normalized
	}
	wr, err := a.writer(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	ev, err := wr.Append(body.Type, body.Payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, ev)
}

func (a *API) SessionState(w http.ResponseWriter, r *http.Request) {
	p, err := a.sessionProjection(r.PathValue("id"))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	raw, _ := json.Marshal(episodic.Fold(p.Events))
	var state map[string]any
	_ = json.Unmarshal(raw, &state)
	state["run"], state["plan_state"], state["report"] = p.Run, p.Plan, p.Report
	state["running"], state["cursor"], state["events"] = p.Running, p.Cursor, p.Events
	state["errors"] = projectErrors(p.Events, p.Run)
	a.qmu.Lock()
	var question any
	if q := a.questions[r.PathValue("id")]; q != nil && !q.Answered && p.Run != nil && q.RunID == p.Run.ID {
		// Marshal while locked: broker may settle the question immediately after.
		data, _ := json.Marshal(q)
		_ = json.Unmarshal(data, &question)
	}
	a.qmu.Unlock()
	state["question"] = question
	writeJSON(w, 200, state)
}

func (a *API) writer(id string) (*episodic.Writer, error) {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	if wr, ok := a.writers[id]; ok {
		return wr, nil
	}
	path := a.sess.EventsPath(id)
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	wr, err := episodic.Open(path)
	if err != nil {
		return nil, err
	}
	a.writers[id] = wr
	return wr, nil
}

func (a *API) Writer(id string) (*episodic.Writer, error) { return a.writer(id) }

func (a *API) Chat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.chat == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "loop not wired"})
		return
	}
	a.sess.Touch(id) // keep an actively-used instant session alive (TTL from last activity)
	// A turn HOLDS the idle clock rather than merely resetting it: a long
	// generation (or an unattended workflow) must not be parked out from
	// under itself mid-run.

	var body struct {
		Text   string      `json:"text"`
		Mode   string      `json:"mode"`
		Images []llm.Image `json:"images,omitempty"`
		// a supervised plan step (RFX_UI planner) is a build task — it runs
		// on the long turn budget, not the conversational one
		Step bool `json:"step"`
		// one-turn sampling override from the chat bar; empty keeps the
		// session default. Temperature is a per-request field — nothing about
		// changing it needs a restart.
		Sampling string `json:"sampling,omitempty"`
	}
	if !decodeBoundedCommand(w, r, &body) {
		return
	}
	a.waitCommand(w, r, loop.Command{Kind: "chat", Text: body.Text, Images: body.Images, Mode: body.Mode, Sampling: body.Sampling})
}

func (a *API) Autopilot(w http.ResponseWriter, r *http.Request) {
	if a.chat == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "loop not wired"})
		return
	}
	a.waitCommand(w, r, loop.Command{Kind: "continue"})
}

func (a *API) SessionReport(w http.ResponseWriter, r *http.Request) {
	p, err := a.sessionProjection(r.PathValue("id"))
	if err != nil || p.Report == nil {
		writeJSON(w, 404, map[string]string{"error": "no plan in this session"})
		return
	}
	writeJSON(w, 200, p.Report)
}
func (a *API) SessionErrors(w http.ResponseWriter, r *http.Request) {
	p, err := a.sessionProjection(r.PathValue("id"))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"errors": projectErrors(p.Events, p.Run)})
}

func (a *API) QuestionBroker() tools.QuestionBroker {
	return func(ctx context.Context, sessionID, question string, options []string) (string, error) {
		loop.SetWaiting(ctx, true)
		defer loop.SetWaiting(ctx, false)
		pq := &pendingQuestion{ID: fmt.Sprintf("%d", time.Now().UnixNano()), RunID: loop.RunID(ctx), Question: question, Options: options, ch: make(chan string, 1)}
		a.qmu.Lock()
		a.questions[sessionID] = pq
		a.qmu.Unlock()
		defer func() {
			a.qmu.Lock()
			if a.questions[sessionID] == pq {
				delete(a.questions, sessionID)
			}
			a.qmu.Unlock()
		}()
		select {
		case ans := <-pq.ch:
			return ans, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (a *API) PendingQuestion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.qmu.Lock()
	pq := a.questions[id]
	a.qmu.Unlock()
	if pq == nil {
		writeJSON(w, http.StatusOK, map[string]any{"question": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": pq.ID, "run_id": pq.RunID, "question": pq.Question, "options": pq.Options})
}

func (a *API) Answer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Answer     string `json:"answer"`
		QuestionID string `json:"question_id"`
		RunID      string `json:"run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Answer == "" || body.QuestionID == "" || body.RunID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "answer, question_id and run_id required"})
		return
	}
	if a.chat == nil {
		writeJSON(w, 503, map[string]string{"error": "loop not wired"})
		return
	}
	err := a.chat.WithRun(id, body.RunID, func() error {
		a.qmu.Lock()
		defer a.qmu.Unlock()
		pq := a.questions[id]
		if pq == nil || pq.Answered || body.QuestionID != pq.ID || body.RunID != pq.RunID {
			return fmt.Errorf("question changed or already answered")
		}
		select {
		case pq.ch <- body.Answer:
			pq.Answered = true
			return nil
		default:
			return fmt.Errorf("answer already delivered")
		}
	})
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "delivered"})
}

func (a *API) Steer(w http.ResponseWriter, r *http.Request) {
	a.runControl(w, r, "steer")
}

func (a *API) Pause(w http.ResponseWriter, r *http.Request) {
	a.runControl(w, r, "pause")
}

func (a *API) Kill(w http.ResponseWriter, r *http.Request) {
	a.runControl(w, r, "kill")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// normalizeErrorCard makes every error event render in the UI card, whether it
// was written as a full card {what,why,tried,options,proposed_fix} or a bare
// {class,detail,stop} guard/boundary event. Fills what/why from detail.
func normalizeErrorCard(payload json.RawMessage) map[string]any {
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return map[string]any{"class": "error", "what": "unparseable error event"}
	}
	if _, ok := m["what"]; !ok {
		// bare event — synthesize a readable card
		detail, _ := m["detail"].(string)
		stop, _ := m["stop"].(string)
		what := detail
		if what == "" {
			what = stop
		}
		if what == "" {
			what = "error"
		}
		m["what"] = what
		if _, ok := m["why"]; !ok && detail != "" {
			m["why"] = detail
		}
	}
	if _, ok := m["class"]; !ok {
		m["class"] = "error"
	}
	return m
}

// SetContextFunc wires the live window budget into /api/status, so the panel
// shows what the Core serves rather than what config.json guessed.
func (a *API) SetContextFunc(f func() int) { a.modelCtx = f }

// SetContextSync wires the window's probe so Health can refresh the budget
// once the Core is known to be up. Health is the one place that already
// knows the Core answered, so it is the one place syncing cannot wake it.
func (a *API) SetContextSync(f func(context.Context)) { a.ctxSync = f }

func (a *API) contextWindow() int {
	if a.modelCtx != nil {
		if n := a.modelCtx(); n > 0 {
			return n
		}
	}
	return a.ConfigSnapshot().ModelCtx
}

// RunPlanStep runs ONE step of the committed plan and verifies it.
//
// POST /api/sessions/{id}/plan/step  {"step": 2, "revision": false}
//
// step is 0-based; -1 (or absent) means "whichever is next", which is what a
// Continue button wants. This exists so a surface can drive a plan without
// composing an English prompt and hoping the model scopes itself: the planner
// panel used to post "do step 3 only, then stop and report" as an ordinary
// turn, so nothing bound the run to step 3 and nothing verified it.
func (a *API) RunPlanStep(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Step     *int   `json:"step"`
		Revision bool   `json:"revision"`
		PlanID   string `json:"plan_event_id"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid step request"})
		return
	}
	step := -1
	if body.Step != nil {
		step = *body.Step
	}
	a.waitCommand(w, r, loop.Command{Kind: "step", Step: step, Revision: body.Revision, PlanID: body.PlanID, Reason: body.Reason})
}

// PlanStateHandler reports the plan and what is known about each step, without
// running anything.
//
// GET /api/sessions/{id}/plan
//
// Unlike /report this carries the cursor — which step is next, which is
// blocked, which revision each is on — so a surface can render controls, not
// just progress.
func (a *API) PlanStateHandler(w http.ResponseWriter, r *http.Request) {
	st, err := a.chat.PlanStateOf(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}
