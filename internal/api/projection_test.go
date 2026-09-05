package api

import (
	"cerveau/internal/episodic"
	"cerveau/internal/loop"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestSessionSnapshotIncludesOnePrefixAndRecoveryIncident(t *testing.T) {
	a, _, sid := setupAPI(t)
	wr, _ := a.Writer(sid)
	wr.Append(episodic.MsgUser, map[string]any{"text": "keep the evidence"})
	ev, _ := wr.Append(episodic.RunState, loop.RunState{ID: "stopped", Status: "running", Phase: "tool_call"})
	req := httptest.NewRequest("GET", "/state", nil)
	req.SetPathValue("id", sid)
	rec := httptest.NewRecorder()
	a.SessionState(rec, req)
	var state struct {
		Cursor  string
		Events  []episodic.Event
		Run     loop.RunState
		Errors  []map[string]any
		Running bool
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || state.Cursor != ev.ID || state.Events[len(state.Events)-1].ID != ev.ID || state.Run.Status != "interrupted" || state.Running || len(state.Errors) != 1 {
		t.Fatalf("snapshot: %s", rec.Body.String())
	}
}

func TestIncidentProjectionKeepsHistoryButOnlyLatestRunIsActionable(t *testing.T) {
	event := func(id string, typ episodic.EventType, payload any) episodic.Event {
		raw, _ := json.Marshal(payload)
		return episodic.Event{ID: id, Type: typ, Payload: raw}
	}
	events := []episodic.Event{
		event("1", episodic.Err, map[string]any{"run_id": "old", "what": "old failure"}),
		event("2", episodic.RunState, loop.RunState{ID: "new", Status: "running", Phase: "accepted"}),
		event("3", episodic.Err, map[string]any{"run_id": "new", "what": "first attempt"}),
		event("4", episodic.Err, map[string]any{"run_id": "new", "what": "exhausted retry"}),
		event("5", episodic.Err, map[string]any{"what": "background warning"}),
	}
	cards := projectErrors(events, &loop.RunState{ID: "new", Status: "failed"})
	if len(cards) != 1 || cards[0]["id"] != "4" {
		t.Fatalf("wrong active incident: %+v", cards)
	}
	if len(projectErrors(events, &loop.RunState{ID: "new", Status: "completed"})) != 0 {
		t.Fatal("resolved incident still active")
	}
	if len(events) != 5 {
		t.Fatal("history erased")
	}
}
