package api

import (
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
	"cerveau/internal/loop"
	"cerveau/internal/memory"
	"cerveau/internal/rfx"
	"cerveau/internal/session"
	"cerveau/internal/skills"
	"cerveau/internal/tools"
)

// Version is the running app version, surfaced in /health.
const Version = "0.4.0-alpha"

type API struct {
	cfg        *config.Config
	configPath string
	sess       session.Store
	http       *http.Client
	chat       *loop.Loop
	sctx       *tools.SessionContext
	ci         *codeintel.Indexer
	mem        *memory.TSClient
	started    time.Time

	wmu     sync.Mutex
	writers map[string]*episodic.Writer

	qmu         sync.Mutex
	questions   map[string]*pendingQuestion
	skillLoader *skills.Loader
	rfxLoader   *rfx.Loader
	wsChange    func(string) error
}

type pendingQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
	ch       chan string
}

func New(cfg *config.Config, sess session.Store) *API {
	return &API{
		cfg:       cfg,
		sess:      sess,
		http:      &http.Client{Timeout: 2 * time.Second},
		writers:   map[string]*episodic.Writer{},
		questions: map[string]*pendingQuestion{},
		started:   time.Now(),
	}
}

// SetConfigPath lets SetRemoteToken persist pairing to disk.
func (a *API) SetConfigPath(p string) { a.configPath = p }

func (a *API) SetLoop(l *loop.Loop) { a.chat = l }

// RemoteToken is the gate's bearer secret (empty = unpaired localhost mode).
func (a *API) RemoteToken() string { return a.cfg.RemoteAccessToken }

// SetRemoteToken persists a freshly minted pairing token into the config.
func (a *API) SetRemoteToken(token string) error {
	a.cfg.RemoteAccessToken = token
	if a.configPath == "" {
		return fmt.Errorf("config path unknown")
	}
	return config.Save(a.configPath, a.cfg)
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
	path, err := pickDirectory(a.cfg.Workspace)
	if err != nil {
		// user cancelled or no picker available — not an error worth alarming on
		writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
		return
	}
	if err := a.wsChange(path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"workspace": a.cfg.Workspace})
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
	if err := a.wsChange(body.Path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "workspace": a.cfg.Workspace})
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
	model := a.ping("model", a.cfg.Endpoints.Model, "/health")
	if model.OK {
		model.Info = a.probeModelName(a.cfg.Endpoints.Model)
	}
	embedder := a.ping("embedder", a.cfg.Endpoints.Embedder, "/health")
	if embedder.OK {
		embedder.Info = a.probeModelName(a.cfg.Endpoints.Embedder)
	}
	ts := a.ping("typesense", a.cfg.Endpoints.Typesense, "/health")
	if ts.OK {
		ts.Info = a.probeTypesenseVersion(a.cfg.Endpoints.Typesense)
	}

	ws := a.cfg.Workspace
	if abs, err := filepath.Abs(ws); err == nil {
		ws = abs
	}
	up := ""
	if !a.started.IsZero() {
		up = time.Since(a.started).Round(time.Second).String()
	}
	// What input types the loaded model actually accepts — read straight from the
	// server's /props (authoritative), not guessed from the model name. Lets the UI
	// show an attach button only when the model can use the attachment.
	var modalities map[string]bool
	if model.OK {
		modalities = a.probeModalities(a.cfg.Endpoints.Model)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"components": []ComponentStatus{model, embedder, ts},
		"workspace":  ws,
		"modes":      []string{"discussion", "brainstorming", "autopilot"},
		"model": map[string]any{
			"name":       model.Info,
			"modalities": modalities, // {text:true, vision:bool, audio:bool, video:bool}
		},
		"system": map[string]any{
			"version":   Version,
			"model_ctx": a.cfg.ModelCtx,
			"uptime":    up,
			"typesense": map[string]any{"managed": a.cfg.TypesenseManaged},
			"sessions":  a.cfg.SessionsDir,
		},
	})
}

// probeModalities reads llama.cpp's /props and reports which input modalities the
// loaded model supports. Text is always true. Vision/audio/video require the model
// to have been loaded with the matching projector (--mmproj), which /props reflects.
func (a *API) probeModalities(base string) map[string]bool {
	out := map[string]bool{"text": true, "vision": false, "audio": false, "video": false}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/props", nil)
	resp, err := a.http.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	var props struct {
		Modalities map[string]bool `json:"modalities"`
	}
	if json.NewDecoder(resp.Body).Decode(&props) == nil {
		for k, v := range props.Modalities {
			out[k] = v
		}
	}
	return out
}

// probeModelName hits /v1/models and returns a cleaned model id.
func (a *API) probeModelName(base string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
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
	req.Header.Set("X-TYPESENSE-API-KEY", a.cfg.TypesenseKey)
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
	id := r.PathValue("id")
	var body struct {
		Type    episodic.EventType `json:"type"`
		Payload json.RawMessage    `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Type == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type and payload required"})
		return
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
	id := r.PathValue("id")
	events, err := episodic.Replay(a.sess.EventsPath(id))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, episodic.Fold(events))
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
	var body struct {
		Text string `json:"text"`
		Mode string `json:"mode"`
		// a supervised plan step (RFX_UI planner) is a build task — it runs
		// on the long turn budget, not the conversational one
		Step bool `json:"step"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text required"})
		return
	}
	// A supervised plan step is a build task: it gets the long budget in the
	// loop guard AND a matching HTTP ceiling (the 5m handler timeout would
	// otherwise kill it first).
	// The loop guard now measures IDLE time, so a productive turn has no
	// fixed duration. The HTTP ceiling is only a backstop against a wedged
	// request — it must be generous enough never to cut live work short.
	httpBudget := 30 * time.Minute
	if body.Step {
		httpBudget = 2 * time.Hour
	}
	ctx, cancel := context.WithTimeout(r.Context(), httpBudget)
	defer cancel()
	if body.Step {
		ctx = loop.WithLongTurn(ctx)
	}
	if a.sctx != nil {
		a.sctx.SessionID = id
		a.sctx.LastEvtID = ""
		if events, err := episodic.Replay(a.sess.EventsPath(id)); err == nil && len(events) > 0 {
			a.sctx.LastEvtID = events[len(events)-1].ID
		}
	}
	res, err := a.chat.Run(ctx, id, body.Text, body.Mode)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) Autopilot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.chat == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "loop not wired"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	res, err := a.chat.RunAutopilot(ctx, id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) SessionReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	events, err := episodic.Replay(a.sess.EventsPath(id))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	rep := loop.BuildReportAt(events, a.cfg.Workspace)
	if rep == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no plan in this session"})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (a *API) SessionErrors(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	events, err := episodic.Replay(a.sess.EventsPath(id))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	cards := []map[string]any{}
	for _, ev := range events {
		if ev.Type == episodic.Err {
			cards = append(cards, normalizeErrorCard(ev.Payload))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"errors": cards})
}

func (a *API) QuestionBroker() tools.QuestionBroker {
	return func(ctx context.Context, sessionID, question string, options []string) (string, error) {
		pq := &pendingQuestion{Question: question, Options: options, ch: make(chan string, 1)}
		a.qmu.Lock()
		a.questions[sessionID] = pq
		a.qmu.Unlock()
		defer func() {
			a.qmu.Lock()
			delete(a.questions, sessionID)
			a.qmu.Unlock()
		}()
		select {
		case ans := <-pq.ch:
			return ans, nil
		case <-ctx.Done():
			return "no answer — decide yourself and note the assumption", nil
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
	writeJSON(w, http.StatusOK, map[string]any{"question": pq.Question, "options": pq.Options})
}

func (a *API) Answer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Answer == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "answer required"})
		return
	}
	a.qmu.Lock()
	pq := a.questions[id]
	a.qmu.Unlock()
	if pq == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no pending question"})
		return
	}
	select {
	case pq.ch <- body.Answer:
	default:
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "delivered"})
}

func (a *API) Steer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text required"})
		return
	}
	wr, err := a.writer(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if _, err := wr.Append(episodic.MsgUser, map[string]any{"text": body.Text, "steer": true}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if a.chat != nil && a.chat.Steer(id) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "steered — re-thinking now"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded (no active run)"})
}

func (a *API) Pause(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.chat != nil && a.chat.Pause(id) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active run"})
}

func (a *API) Kill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.chat != nil && a.chat.Kill(id) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "killed"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active run"})
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
