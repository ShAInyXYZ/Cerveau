package loop

import (
	"context"
	"encoding/json"
	"testing"

	"cerveau/internal/episodic"
)

func TestManualReflexIdentitySurvivesFailureAndReplay(t *testing.T) {
	l, _, done := stepRunFixture(t)
	defer done()
	_, err := l.RunReflexFor(context.Background(), "s1", "missing-reflex", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected unknown Reflex failure")
	}
	state := l.RunStateOf("s1")
	if state == nil || state.Kind != "reflex" || state.Reflex != "missing-reflex" || state.Status != "failed" {
		t.Fatalf("lost manual action identity: %+v", state)
	}
	events, err := episodic.Replay(l.path("s1"))
	if err != nil {
		t.Fatal(err)
	}
	states := 0
	for _, event := range events {
		if event.Type != episodic.RunState {
			continue
		}
		var saved RunState
		if err := json.Unmarshal(event.Payload, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Kind != "reflex" || saved.Reflex != "missing-reflex" {
			t.Fatalf("unidentified journal state: %+v", saved)
		}
		states++
	}
	if states < 2 {
		t.Fatalf("expected admission and terminal states, got %d", states)
	}
}
