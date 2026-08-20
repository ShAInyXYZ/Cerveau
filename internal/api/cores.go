package api

import (
	"encoding/json"
	"net/http"

	"cerveau/internal/config"
	"cerveau/internal/cores"
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
	live := a.cfg.Endpoints.Model
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
	a.cfg.Endpoints.Model = c.Endpoint
	if a.configPath != "" {
		_ = config.Save(a.configPath, a.cfg)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"core":    c,
		"restart": true,
		"note":    "the endpoint is switched; restart Cerveau so every component picks it up",
	})
}
