package api

import (
	"encoding/json"
	"net/http"
	"time"

	"cerveau/internal/cores"
	"cerveau/internal/rig"
)

// GET /api/rig[?core=<id>] — the machine, and where one profile sits on it.
//
// Read-only. `observed` is what the driver reports on each card right now;
// `next` is what the chosen profile runs with on its next start. They are
// returned side by side because they legitimately disagree — a parked Core, a
// saved parameter not yet applied, or a profile that is not the loaded one —
// and the panel shows that rather than hiding it. Live temperature and load
// stay in /api/system/stats.
//
// Without ?core the profile is the live one, resolved the way ListCores does
// it: by the endpoint the harness is really talking to, then the stored choice.
func (a *API) Rig(w http.ResponseWriter, r *http.Request) {
	reg, err := cores.Load(cores.DefaultPath())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	live := reg.ByEndpoint(a.ConfigSnapshot().Endpoints.Model)
	if live == nil {
		live = reg.ActiveCore()
	}
	c, liveID := live, ""
	if live != nil {
		liveID = live.ID
	}
	// resolve the profile before touching the driver: a bad id costs nothing
	if id := r.URL.Query().Get("core"); id != "" {
		if c = reg.ByID(id); c == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no core with id " + id})
			return
		}
	}
	inv := rig.Probe()
	var next *rig.Placement
	if c != nil {
		ov, _ := cores.LoadOverrides(cores.OverridesPath(c.ID))
		eov, _ := cores.LoadOverrides(cores.EmbedOverridesPath(c.ID))
		p := rig.Declared(c, ov, eov, inv)
		p.Live = c.ID == liveID
		next = &p
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"inventory": inv,
		"observed":  rig.Observed(inv, reg),
		"next":      next,
		// every profile, so the panel can offer them without a second request
		"profiles": rig.Profiles(reg, liveID),
	})
}

// factsTimeout bounds reading a checkpoint that may sit on a network mount.
const factsTimeout = 3 * time.Second

// POST /api/rig/plan — what saving a drawn layout would write, and whether it fits.
//
// A what-if: nothing is saved here. The answer is the complete override sets
// for PUT /api/cores/{id}/params, what would change, what stands in the way,
// and an estimate of the fit from the profile's own checkpoint on disk. The
// panel draws; the rules live in internal/rig, so they are the same whatever
// draws next.
func (a *API) RigPlan(w http.ResponseWriter, r *http.Request) {
	var req rig.LayoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Core == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "core, gpus and embed required"})
		return
	}
	reg, err := cores.Load(cores.DefaultPath())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c := reg.ByID(req.Core)
	if c == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no core with id " + req.Core})
		return
	}
	ov, _ := cores.LoadOverrides(cores.OverridesPath(c.ID))
	eov, _ := cores.LoadOverrides(cores.EmbedOverridesPath(c.ID))
	inv := rig.Probe()
	use := rig.Observed(inv, reg)
	facts, factsErr := rig.ReadFactsWithin(cores.Effective(c.Params, ov)["MODEL"], factsTimeout)
	plan := rig.PlanLayout(rig.PlanInput{
		Core: c, Overrides: ov, EmbedOverrides: eov, Inventory: inv, Facts: facts, EmbedMiB: rig.EmbedderMemory(use),
	}, req)
	if len(plan.GPUs) > 0 {
		fit := rig.Estimate(facts, inv, rig.BusyMemory(use), plan.GPUs, plan.GPUUtil, plan.KV, plan.Window)
		if factsErr != nil {
			fit.Reasons = []string{factsErr.Error()}
		}
		plan.Fit = &fit
	}
	writeJSON(w, http.StatusOK, plan)
}

// GET /api/rig/layouts?core=<id> · PUT /api/rig/layouts · DELETE /api/rig/layouts?core=&name=
//
// "Save as": named placements kept beside a profile. Loading one only fills
// the editor — PUT /api/cores/{id}/params is still the one thing that changes
// what a Core runs with. The two that write take JSON only, like every other
// endpoint a sandboxed panel must not be able to reach.
func (a *API) RigLayouts(w http.ResponseWriter, r *http.Request) {
	path := rig.LayoutsPath()
	core := r.URL.Query().Get("core")
	switch r.Method {
	case http.MethodPut:
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			Core   string          `json:"core"`
			Layout rig.SavedLayout `json:"layout"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Core == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "core and layout required"})
			return
		}
		if err := rig.SaveLayout(path, body.Core, body.Layout); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		core = body.Core
	case http.MethodDelete:
		if !requireJSON(w, r) {
			return
		}
		if err := rig.DeleteLayout(path, core, r.URL.Query().Get("name")); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	all, err := rig.LoadLayouts(path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	layouts := all[core]
	if layouts == nil {
		layouts = []rig.SavedLayout{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"core": core, "layouts": layouts})
}
