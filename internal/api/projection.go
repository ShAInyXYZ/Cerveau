package api

import (
	"cerveau/internal/episodic"
	"cerveau/internal/loop"
	"encoding/json"
)

func (a *API) sessionProjection(id string) (*loop.Projection, error) {
	if a.chat != nil {
		return a.chat.Snapshot(id)
	}
	events, err := episodic.Replay(a.sess.EventsPath(id))
	if err != nil {
		return nil, err
	}
	return loop.ProjectEvents(events, ""), nil
}

// Historical failures remain in events/logs; only the latest unsettled run
// produces active cards. A recoverable retry is activity, not another incident.
func projectErrors(events []episodic.Event, run *loop.RunState) []map[string]any {
	cards := []map[string]any{}
	if run != nil && (loop.ActiveRunStatus(run.Status) || run.Status == "completed" || run.Status == "cancelled") {
		return cards
	}
	start := 0
	for i, ev := range events {
		if ev.Type == episodic.RunState {
			var st loop.RunState
			if json.Unmarshal(ev.Payload, &st) == nil && st.Phase == "accepted" {
				start = i
			}
		}
	}
	for _, ev := range events[start:] {
		if ev.Type != episodic.Err {
			continue
		}
		var scope struct {
			RunID string `json:"run_id"`
		}
		_ = json.Unmarshal(ev.Payload, &scope)
		if run != nil && scope.RunID != run.ID {
			continue
		}
		card := normalizeErrorCard(ev.Payload)
		card["id"] = ev.ID
		card["run_id"] = scope.RunID
		cards = append(cards, card)
	}
	if run != nil && (run.Status == "failed" || run.Status == "interrupted" || run.Status == "suspended") {
		// One current action target, even when several attempts failed historically.
		if len(cards) > 0 {
			return cards[len(cards)-1:]
		}
		cards = append(cards, map[string]any{"id": run.ID, "run_id": run.ID, "class": run.Status, "what": run.Reason})
	}
	return cards
}
