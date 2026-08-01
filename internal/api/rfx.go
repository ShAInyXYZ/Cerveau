package api

import (
	"encoding/json"
	"net/http"

	"cerveau/internal/rfx"
	"cerveau/internal/tools"
)

// rfxLoader is wired via SetRfxLoader (mirrors SetSkillLoader).

type rfxReflexView struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Kind        string         `json:"kind"`
	Risk        string         `json:"risk"`
	Modes       []string       `json:"modes"`
	Pack        string         `json:"pack"`
	Enabled     bool           `json:"enabled"`
	Params      map[string]any `json:"params,omitempty"`
}

type rfxPackView struct {
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Author      string     `json:"author"`
	Description string     `json:"description"`
	Icon        string     `json:"icon,omitempty"`
	HasPanel    bool       `json:"has_panel,omitempty"`
	Docs        []string   `json:"docs"`
	UI          rfx.PackUI `json:"ui,omitempty"`
}

// ListRfx serves the Settings → RFX section: packs, reflexes with on/OFF
// state, loader notices and rejections. T0-friendly (no model needed).
func (a *API) ListRfx(w http.ResponseWriter, r *http.Request) {
	if a.rfxLoader == nil {
		writeJSON(w, http.StatusOK, map[string]any{"packs": []any{}, "reflexes": []any{}})
		return
	}
	packs := []rfxPackView{}
	for _, p := range a.rfxLoader.Packs() {
		packs = append(packs, rfxPackView{p.Pack, p.Version, p.Author, p.Description, p.Icon, p.Panel != "", p.Docs, p.UI})
	}
	reflexes := []rfxReflexView{}
	for _, d := range a.rfxLoader.All() {
		modes := d.Modes
		if len(modes) == 0 {
			modes = rfx.Modes
		}
		reflexes = append(reflexes, rfxReflexView{
			Name: d.Name, Description: d.Description, Kind: d.Kind, Risk: d.Risk,
			Modes: modes, Pack: d.Pack, Enabled: !a.rfxLoader.Disabled(d.Name),
			Params: d.Params,
		})
	}
	notices := []string{}
	for _, n := range a.rfxLoader.Notices() {
		notices = append(notices, n)
	}
	errors := []string{}
	for _, e := range a.rfxLoader.Errors() {
		errors = append(errors, e.Path+": "+e.Err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"packs": packs, "reflexes": reflexes, "notices": notices, "errors": errors,
	})
}

// ToggleRfx flips one reflex's enabled state (.state.json — manifests are
// never edited). Grammar follows on the next turn.
func (a *API) ToggleRfx(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	if a.rfxLoader == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rfx not wired"})
		return
	}
	var body struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if err := a.rfxLoader.SetEnabled(body.Name, body.Enabled); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": body.Name, "enabled": body.Enabled})
}

// RunRfx is the RFX_UI dock's action bridge: fire one reflex manually with
// args, through the guarded registry path. The output returns to the card.
func (a *API) RunRfx(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	if a.chat == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "loop not wired"})
		return
	}
	var body struct {
		Name      string          `json:"name"`
		Args      json.RawMessage `json:"args"`
		Confirmed bool            `json:"confirmed"` // an explicit UI confirm click
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if len(body.Args) == 0 {
		body.Args = json.RawMessage(`{}`)
	}
	ctx := r.Context()
	if body.Confirmed {
		// The user clicked through a confirm strip / arm step: sensitive-tier
		// guard rules treat that as their required confirmation.
		ctx = tools.WithHumanApproval(ctx)
	}
	out, err := a.chat.RunReflex(ctx, body.Name, body.Args)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "output": out, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}
