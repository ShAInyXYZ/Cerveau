package api

import (
	"cerveau/internal/loop"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func (a *API) Command(w http.ResponseWriter, r *http.Request) {
	if a.chat == nil {
		writeJSON(w, 503, map[string]string{"error": "loop not wired"})
		return
	}
	var cmd loop.Command
	if !decodeBoundedCommand(w, r, &cmd) {
		return
	}
	id := r.PathValue("id")
	if _, err := a.writer(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "session not found"})
		return
	}
	var release func()
	if a.idle != nil {
		release = a.idle.Hold()
	}
	run, err := a.chat.Start(id, cmd, release)
	if err != nil {
		if release != nil {
			release()
		}
		code := 400
		if errors.Is(err, loop.ErrBusy) {
			code = 409
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	a.sess.Touch(id)
	writeJSON(w, 202, map[string]any{"run": run})
}
func (a *API) Resume(w http.ResponseWriter, r *http.Request) {
	a.runControl(w, r, "resume")
}

func (a *API) runControl(w http.ResponseWriter, r *http.Request, action string) {
	if a.chat == nil {
		writeJSON(w, 503, map[string]string{"error": "loop not wired"})
		return
	}
	var body struct {
		RunID   string  `json:"run_id"`
		ID      string  `json:"control_id"`
		Version *uint64 `json:"control_version"`
		Text    string  `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RunID == "" || body.ID == "" || body.Version == nil {
		writeJSON(w, 400, map[string]string{"error": "run_id, control_id and control_version required"})
		return
	}
	id := r.PathValue("id")
	if _, err := a.sess.Get(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "session not found"})
		return
	}
	run, err := a.chat.Control(id, action, loop.RunControl{RunID: body.RunID, ID: body.ID, Version: *body.Version, Text: body.Text})
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, loop.ErrControlConflict) {
			code = http.StatusConflict
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"status": run.Status, "run": run})
}

func (a *API) Build(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.buildInfo())
}

func (a *API) waitCommand(w http.ResponseWriter, r *http.Request, cmd loop.Command) {
	if a.chat == nil {
		writeJSON(w, 503, map[string]string{"error": "loop not wired"})
		return
	}
	id := r.PathValue("id")
	if _, err := a.writer(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "session not found"})
		return
	}
	cmd.ID = r.Header.Get("Idempotency-Key")
	if cmd.ID == "" {
		cmd.ID = fmt.Sprintf("legacy-%d", time.Now().UnixNano())
	}
	var release func()
	if a.idle != nil {
		release = a.idle.Hold()
	}
	accepted, err := a.chat.Start(id, cmd, release)
	if err != nil {
		if release != nil {
			release()
		}
		code := 400
		if errors.Is(err, loop.ErrBusy) {
			code = 409
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		st := a.chat.RunStateOf(id)
		if st != nil && st.ID == accepted.ID && (st.Status == "completed" || st.Status == "failed" || st.Status == "suspended" || st.Status == "cancelled" || st.Status == "interrupted") {
			if st.Result != nil {
				writeJSON(w, 200, st.Result)
			} else {
				writeJSON(w, 502, map[string]string{"error": st.Reason, "run_id": st.ID})
			}
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}
