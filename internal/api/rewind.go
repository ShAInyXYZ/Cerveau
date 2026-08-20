package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"cerveau/internal/episodic"
)

// rewindTo truncates the episodic log so that everything from `id` onward is
// gone, leaving the conversation as it stood immediately before that event.
//
// This is what "edit a message" means for an append-only log: the edited text
// cannot simply be patched in place, because every turn AFTER it was a response
// to the original. Keeping both would leave the model reading a question and a
// correction with no way to know which one counts.
//
// SCOPE: this rewinds the CONVERSATION only. Files the model wrote or edited
// after that point stay on disk, untouched.
//
// That asymmetry is deliberate. Deleting them would be far worse: the model may
// have edited files the user also touched, the workspace may be a git repo with
// its own history, and "undo three writes" is not reliably invertible from a
// log. Silently reverting someone's workspace because they fixed a typo in a
// prompt is indefensible; leaving the files is merely surprising, and the panel
// says so before the edit is committed.
//
// The whole file is rewritten rather than truncated at an offset: the offset of
// a given event is not recorded anywhere, and recomputing it from line lengths
// would break the moment an event contains an escaped newline.
func rewindTo(path, id string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var kept []string
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		var ev struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.ID == id {
			found = true
			break // this event and everything after it goes
		}
		kept = append(kept, line)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// Refuse rather than truncate to nothing: an id that is not in this log is
	// a bug or a stale client, and silently erasing the session would be the
	// worst possible response to it.
	if !found {
		return fmt.Errorf("event %q is not in this session", id)
	}

	tmp := path + ".rewind"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, l := range kept {
		if _, err := out.WriteString(l + "\n"); err != nil {
			out.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// POST /api/sessions/{id}/rewind — drop an event and everything after it.
//
// Used by "edit this message": the panel rewinds to the message being edited,
// then sends the new text as a fresh turn.
func (a *API) Rewind(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("id")
	var body struct {
		EventID string `json:"event_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.EventID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event_id required"})
		return
	}
	// Never rewrite a log that a turn is actively appending to.
	if a.chat != nil {
		for _, running := range a.chat.RunningSessions() {
			if running == sid {
				writeJSON(w, http.StatusConflict,
					map[string]string{"error": "a turn is running in this session — stop it first"})
				return
			}
		}
	}
	if err := rewindTo(a.sess.EventsPath(sid), body.EventID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	events, _ := episodic.Replay(a.sess.EventsPath(sid))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "remaining": len(events)})
}
