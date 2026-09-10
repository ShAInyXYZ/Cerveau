package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"cerveau/internal/cores"
	"cerveau/internal/idle"
)

// GET /api/idle — what the panel needs to render the Idle screen.
//
// The countdown is served rather than computed in the browser so a reloaded
// panel, a second device, and the harness all agree on when the park lands.
func (a *API) IdleStatus(w http.ResponseWriter, r *http.Request) {
	if a.idle == nil {
		writeJSON(w, http.StatusOK, map[string]any{"state": "active", "enabled": false})
		return
	}
	a.RefreshIdle(r.Context())
	writeJSON(w, http.StatusOK, a.idle.Status())
}

// RefreshIdle is shared by polling and the timer, so a closed browser cannot
// leave the tracker repeatedly requesting parks after one already completed.
// Return the fresh service observation, which can be unknown. The tracker keeps
// its last display state across transient metadata failures; that remembered
// state is not permission for Health to contact a socket-activated Core.
func (a *API) RefreshIdle(ctx context.Context) idle.State {
	if a.idle == nil {
		return ""
	}
	a.idleObserveMu.Lock()
	defer a.idleObserveMu.Unlock()
	var state idle.State
	if a.idleCoreState != nil {
		state = a.idleCoreState(ctx)
	} else if a.cfg != nil {
		if reg, err := cores.Load(cores.DefaultPath()); err == nil {
			if core := reg.ByEndpoint(a.ConfigSnapshot().Endpoints.Model); core != nil && core.Unit != "" {
				state = idle.ServiceState(ctx, core.Unit)
			}
		}
	}
	a.idle.Observe(state)
	return state
}

// POST /api/idle/stay — "I'm still here." Pushes the park out without turning
// the feature off, which is what a user at the desk actually wants.
func (a *API) IdleStay(w http.ResponseWriter, r *http.Request) {
	if a.idle == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "idle not wired"})
		return
	}
	var body struct {
		Minutes int `json:"minutes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	d := time.Duration(body.Minutes) * time.Minute
	if d <= 0 {
		d = a.idle.Cfg().After
	}
	// A pending request may already be on disk if the user answered late;
	// clearing it is what makes "stay awake" actually mean it.
	a.idle.ClearRequest()
	a.idle.Snooze(d)
	writeJSON(w, http.StatusOK, a.idle.Status())
}

// POST /api/idle/now — "yes, go idle" from the warning screen.
func (a *API) IdleNow(w http.ResponseWriter, r *http.Request) {
	if a.idle == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "idle not wired"})
		return
	}
	if err := a.idle.ParkNow(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a.idle.Status())
}

// POST /api/idle/config — change the timings live.
func (a *API) IdleConfig(w http.ResponseWriter, r *http.Request) {
	if a.idle == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "idle not wired"})
		return
	}
	var body struct {
		AfterMinutes *int  `json:"after_minutes"`
		WarnMinutes  *int  `json:"warn_minutes"`
		Enabled      *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	c := a.idle.Cfg()
	if body.AfterMinutes != nil && *body.AfterMinutes > 0 {
		c.After = time.Duration(*body.AfterMinutes) * time.Minute
	}
	if body.WarnMinutes != nil && *body.WarnMinutes >= 0 {
		c.Warn = time.Duration(*body.WarnMinutes) * time.Minute
	}
	if body.Enabled != nil {
		c.Enabled = *body.Enabled
	}
	// A warning longer than the timeout would mean the screen is up from the
	// first second of silence, which reads as broken rather than helpful.
	if c.Warn >= c.After {
		c.Warn = c.After / 3
	}
	a.idle.SetConfig(c)
	writeJSON(w, http.StatusOK, a.idle.Status())
}

// SetIdle wires the tracker in. Kept separate from New so a harness without
// power management still builds and runs.
func (a *API) SetIdle(t *idle.Tracker) { a.idle = t }
