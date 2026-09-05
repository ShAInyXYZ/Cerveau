package api

import (
	"encoding/json"
	"net/http"

	"cerveau/internal/config"
	"cerveau/internal/cores"
	"cerveau/internal/llm"
)

// GET /api/cores — the Brain Cores this machine knows about, which one is
// answering right now, and whether it is reachable.
//
// "Active" is resolved by ENDPOINT, not by the stored `active` field: the field
// records intent, the endpoint records reality, and after a manual engine
// restart those can disagree. Showing intent as if it were reality is how a
// panel ends up confidently naming the wrong engine.
func (a *API) ListCores(w http.ResponseWriter, r *http.Request) {
	reg, err := cores.Load(cores.DefaultPath())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	live := a.ConfigSnapshot().Endpoints.Model
	activeID := ""
	if c := reg.ByEndpoint(live); c != nil {
		activeID = c.ID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cores":    reg.Cores,
		"active":   activeID,
		"endpoint": live,
		// intent, kept separate so a mismatch is visible rather than hidden
		"selected": reg.Active,
		// the watchdog's progress on the last switch, if one is recent
		"switch": cores.ReadSwitchStatus(),
	})
}

// POST /api/cores/select — point the harness at a different Core.
//
// This does NOT start or stop engines. Bringing one up is a command the user
// runs; a harness that can stop the engine it is talking to has a failure mode
// where the machine ends up with no model at all, and no way to say so. The
// response carries the start command for the chosen Core so the panel can show
// exactly what to run.
func (a *API) SelectCore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	path := cores.DefaultPath()
	reg, err := cores.Load(path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := reg.SetActive(body.ID, path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c := reg.ActiveCore()

	// Point the running harness at the new endpoint and persist it, so a
	// restart of crv comes up on the same Core.
	if err := a.updateConfig(func(next *config.Config) { next.Endpoints.Model = c.Endpoint }, nil, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"core":    c,
		"restart": true,
		"note":    "the endpoint is switched; restart Cerveau so every component picks it up",
	})
}

// GET /api/sampling — global defaults for future runs and what a UI may offer.
func (a *API) GetSampling(w http.ResponseWriter, r *http.Request) {
	name := "strict"
	if a.chat != nil {
		name = a.chat.SamplingName()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active":  name,
		"presets": llm.PresetNames(),
	})
}

// POST /api/sampling — persist the default for future runs. An accepted run
// retains its frozen sampling snapshot; changing this never retunes it mid-run.
func (a *API) SetSampling(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if a.chat == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "loop not wired"})
		return
	}
	valid := false
	for _, p := range llm.PresetNames() {
		if body.Name == p {
			valid = true
		}
	}
	if !valid {
		writeJSON(w, 400, map[string]string{"error": "invalid sampling preset"})
		return
	}
	if err := a.updateConfig(func(next *config.Config) { next.Sampling = body.Name }, func() { a.chat.SetSampling(body.Name) }, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": body.Name})
}

// GET /api/cores/{id}/params — what a Core runs with: the profile's defaults
// (its unit's Environment= lines, as install.sh recorded them), the user's
// overrides, and the effective set the next start will use.
//
// Choosing a Core in the panel already switches all of this — every profile is
// its own unit. What was missing was seeing it, and changing it without
// editing a unit file by hand.
func (a *API) CoreParams(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reg, err := cores.Load(cores.DefaultPath())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c := reg.ByID(id)
	if c == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no core with id " + id})
		return
	}
	a.writeParams(w, c)
}

// PUT /api/cores/{id}/params — replace the overrides for one Core.
//
// Writes a file the Core's systemd unit reads at its next start. Nothing is
// restarted here, on purpose: the same invariant as SelectCore. A park/wake
// cycle or a manual restart applies it, and the response says so.
func (a *API) SetCoreParams(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Overrides      map[string]string `json:"overrides"`
		EmbedOverrides map[string]string `json:"embed_overrides"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "overrides object required"})
		return
	}
	reg, err := cores.Load(cores.DefaultPath())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c := reg.ByID(id)
	if c == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no core with id " + id})
		return
	}
	// Only keep what differs from the profile: the file is a diff, so a reset
	// to defaults is an empty file, which SaveOverrides turns into no file.
	ov := map[string]string{}
	for k, v := range body.Overrides {
		if d, ok := c.Params[k]; ok && d == v {
			continue
		}
		ov[k] = v
	}
	if err := cores.SaveOverrides(cores.OverridesPath(id), ov); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.EmbedOverrides != nil {
		eo := map[string]string{}
		for k, v := range body.EmbedOverrides {
			if d, ok := c.Embed[k]; ok && d == v {
				continue
			}
			eo[k] = v
		}
		if err := cores.SaveOverrides(cores.EmbedOverridesPath(id), eo); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	a.writeParams(w, c)
}

func (a *API) writeParams(w http.ResponseWriter, c *cores.Core) {
	path := cores.OverridesPath(c.ID)
	ov, err := cores.LoadOverrides(path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	eov, _ := cores.LoadOverrides(cores.EmbedOverridesPath(c.ID))
	writeJSON(w, http.StatusOK, map[string]any{
		"id":        c.ID,
		"defaults":  c.Params,
		"overrides": ov,
		"effective": cores.Effective(c.Params, ov),
		"file":      path,
		// the embedder's placement under this Core — same shape, own file
		"embed": map[string]any{
			"defaults":  c.Embed,
			"overrides": eov,
			"effective": cores.Effective(c.Embed, eov),
		},
		"applies": "on Restart — the Core reloads with these, and the embedder is restarted with its own",
	})
}

// POST /api/cores/apply — choose a Core AND ask for it to be brought up.
//
// Same registry write as SelectCore, then a request file for the park
// watchdog: stop every other Core's unit, start this one, restart Cerveau.
// The harness still never runs systemctl; that invariant is what keeps a
// crash mid-switch from stranding the machine with no model. A Core without a
// `unit` in cores.json cannot be switched this way — the response carries the
// commands instead, exactly as the old restart prompt did.
func (a *API) ApplyCore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	path := cores.DefaultPath()
	reg, err := cores.Load(path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := reg.SetActive(body.ID, path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c := reg.ActiveCore()
	if err := a.updateConfig(func(next *config.Config) { next.Endpoints.Model = c.Endpoint }, nil, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if c.Unit == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"core": c, "manual": true, "restart": true,
			"note": "this Core has no systemd unit in cores.json — start it and restart Cerveau by hand",
		})
		return
	}
	var stop []string
	for _, o := range reg.Cores {
		if o.ID != c.ID && o.Unit != "" {
			stop = append(stop, o.Unit)
		}
	}
	// The embedder follows the Core: write its effective settings for
	// cerveau-embed.service and have the watchdog restart it. A Core with no
	// `embed` clears the file, which is the unit's own CPU default.
	eov, _ := cores.LoadOverrides(cores.EmbedOverridesPath(c.ID))
	if err := cores.WriteEmbedEnv(cores.Effective(c.Embed, eov)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "embed.env: " + err.Error()})
		return
	}
	req := cores.SwitchRequest{Core: c.ID, Unit: c.Unit, Stop: stop, RestartCerveau: true, RestartEmbed: true}
	if err := cores.WriteSwitchRequest(req); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"core": c, "manual": false, "applying": true,
		"status": cores.ReadSwitchStatus(),
		"note":   "the park watchdog stops the other Cores, starts this one and restarts Cerveau; poll /api/cores for `switch`",
	})
}

// GET /api/thinking — which modes reason before answering, and how hard.
func (a *API) GetThinking(w http.ResponseWriter, r *http.Request) {
	mode, effort := "plan", llm.ThinkingLow
	if a.chat != nil {
		mode, effort = a.chat.Thinking()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":    mode,
		"effort":  effort,
		"modes":   []string{"off", "plan", "autopilot", "always"},
		"efforts": []string{llm.ThinkingLow, llm.ThinkingMedium, llm.ThinkingXHigh},
	})
}

// POST /api/thinking — persist defaults for future runs. Already-accepted runs
// retain the mode and effort recorded in their acceptance snapshot.
func (a *API) SetThinking(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode   string `json:"mode"`
		Effort string `json:"effort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode and effort required"})
		return
	}
	if a.chat == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "loop not wired"})
		return
	}
	if (body.Mode != "off" && body.Mode != "plan" && body.Mode != "autopilot" && body.Mode != "always") || !llm.ValidThinking(body.Effort) || body.Effort == "off" {
		writeJSON(w, 400, map[string]string{"error": "invalid thinking mode or effort"})
		return
	}
	if err := a.updateConfig(func(next *config.Config) {
		next.ThinkingMode, next.ThinkingEffort = body.Mode, body.Effort
	}, func() { a.chat.SetThinking(body.Mode, body.Effort) }, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": body.Mode, "effort": body.Effort})
}
