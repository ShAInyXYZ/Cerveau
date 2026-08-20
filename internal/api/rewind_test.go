package api

import (
	"os"
	"path/filepath"
	"testing"

	"cerveau/internal/episodic"
)

// Editing a message means the conversation continues from THAT point: the
// message and everything after it are dropped, and the edited text is sent
// fresh. Without this the model sees both the original and the correction and
// has to guess which one counts.
func TestRewindDropsTheEventAndEverythingAfter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, err := episodic.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, txt := range []string{"first", "second", "third"} {
		ev, err := w.Append(episodic.MsgUser, map[string]string{"text": txt})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ev.ID)
	}
	w.Close()

	if err := rewindTo(path, ids[1]); err != nil {
		t.Fatalf("rewind: %v", err)
	}
	events, err := episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 surviving event, got %d", len(events))
	}
	if events[0].ID != ids[0] {
		t.Errorf("wrong event survived: %s", events[0].ID)
	}
}

// An unknown id must leave the log untouched rather than truncating to nothing.
func TestRewindToUnknownIDIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	w, _ := episodic.Open(path)
	w.Append(episodic.MsgUser, map[string]string{"text": "only"})
	w.Close()
	before, _ := os.ReadFile(path)

	if err := rewindTo(path, "evt_nope"); err == nil {
		t.Fatal("expected an error for an unknown event id")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("the log was modified despite the failure")
	}
}
