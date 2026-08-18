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
	Self         bool   `json:"self,omitempty"` // the device making this request
}

// serveDevices handles GET /api/devices and POST /api/devices/revoke.
func serveDevices(w http.ResponseWriter, r *http.Request) {
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
		removed, err := revokeDevice(body.ID, body.Cascade)
		if err != nil {
			http.Error(w, "revoke failed", http.StatusInternalServerError)
			return
		}
		if len(removed) == 0 {
			http.Error(w, "no such device", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"removed": removed})
		return
	}

	out := []deviceView{}
	for _, d := range loadDevices() {
		out = append(out, deviceView{
			ID: d.ID, Label: d.Label, AddedAt: d.AddedAt,
			ApprovedBy: d.ApprovedBy, ApproverGone: d.ApproverGone,
			Self: d.ID == me,
		})
	}
	writeJSON(w, map[string]any{"devices": out})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
