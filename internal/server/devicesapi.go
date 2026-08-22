package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// The device list and revocation, exposed to the panel.
//
// These routes sit BEHIND the full gate (bearer token + live device
// signature), so listing or revoking requires an already-trusted device.
// A phone can therefore manage the fleet it helped build, which is the point
// of letting it vouch in the first place.

type deviceView struct {
	ID           string `json:"id"`
	Label        string `json:"label,omitempty"`
	AddedAt      string `json:"added_at"`
	ApprovedBy   string `json:"approved_by,omitempty"`
	ApproverGone bool   `json:"approver_gone,omitempty"`
	LastSeen     string `json:"last_seen,omitempty"`
	Self         bool   `json:"self,omitempty"` // the device making this request
}

// registerDeviceRoutes puts the fleet endpoints on the router.
//
// They used to exist ONLY inside authGate, after the full token+signature
// proof. That works for a remote phone, but a loopback request short-circuits
// above that branch — so on the machine running Cerveau the routes 404'd and
// the desktop panel could never list or revoke devices. Registering them makes
// the local case work; authGate still intercepts remote requests first and
// applies the full proof, so nothing is loosened.
func registerDeviceRoutes(mux *http.ServeMux, cfg authCfg) {
	h := func(w http.ResponseWriter, r *http.Request) { serveDevices(cfg, w, r) }
	mux.HandleFunc("/api/devices", h)
	mux.HandleFunc("/api/devices/revoke", h)
}

// serveDevices handles GET /api/devices and POST /api/devices/revoke.
func serveDevices(cfg authCfg, w http.ResponseWriter, r *http.Request) {
	me := r.Header.Get("X-Cerveau-Device")

	if strings.HasSuffix(r.URL.Path, "/revoke") {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			ID      string `json:"id"`
			Cascade bool   `json:"cascade"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Revoking the device you are currently using would drop your own
		// access mid-request and leave no way back in except the console.
		// Refuse rather than half-perform it.
		if body.ID == me {
			http.Error(w, "cannot revoke the device you are using", http.StatusConflict)
			return
		}
		removed, remaining, err := revokeDevice(body.ID, body.Cascade)
		if err != nil {
			http.Error(w, "revoke failed", http.StatusInternalServerError)
			return
		}
		if len(removed) == 0 {
			http.Error(w, "no such device", http.StatusNotFound)
			return
		}
		// A bearer token with no trusted device left behind it is a shared
		// secret nobody holds — retire it so a leaked-but-unrevoked token
		// cannot pair a fresh attacker device. Clearing it returns the
		// server to its unpaired state; the next pair mints a new token.
		tokenCleared := false
		if remaining == 0 {
			if err := cfg.SetRemoteToken(""); err != nil {
				http.Error(w, "revoke saved but token rotation failed", http.StatusInternalServerError)
				return
			}
			tokenCleared = true
		}
		writeJSON(w, map[string]any{"removed": removed, "token_cleared": tokenCleared})
		return
	}

	out := []deviceView{}
	for _, d := range loadDevices() {
		out = append(out, deviceView{
			ID: d.ID, Label: d.Label, AddedAt: d.AddedAt,
			ApprovedBy: d.ApprovedBy, ApproverGone: d.ApproverGone,
			LastSeen: d.LastSeen,
			Self:     d.ID == me,
		})
	}
	writeJSON(w, map[string]any{"devices": out})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
