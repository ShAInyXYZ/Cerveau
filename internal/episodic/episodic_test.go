package episodic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpEvents(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "s1", "events.jsonl")
}

func TestAppendMonotonic(t *testing.T) {
	path := tmpEvents(t)
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ids := []string{}
	for _, typ := range []EventType{MsgUser, MsgAssistant, ToolCall} {
		ev, err := w.Append(typ, map[string]string{"text": "hello"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ev.ID)
	}
	want := []string{"evt_000001", "evt_000002", "evt_000003"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("id %d = %s, want %s", i, ids[i], want[i])
		}
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("file has %d lines, want 3", len(lines))
	}
}

func TestResumeAfterReopen(t *testing.T) {
	path := tmpEvents(t)
	w, _ := Open(path)
	w.Append(MsgUser, map[string]string{"text": "a"})
	w.Append(MsgAssistant, map[string]string{"text": "b"})
	w.Close()

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	ev, err := w2.Append(Note, map[string]string{"text": "c"})
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "evt_000003" {
		t.Fatalf("resumed id = %s, want evt_000003", ev.ID)
	}
}

func TestPartialTailTruncated(t *testing.T) {
	path := tmpEvents(t)
	w, _ := Open(path)
	w.Append(MsgUser, map[string]string{"text": "a"})
	w.Append(MsgAssistant, map[string]string{"text": "b"})
	w.Close()

	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString("{\"id\":\"evt_000003\",\"ts\":\"2026-01-01")
	f.Close()

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	ev, err := w2.Append(Note, map[string]string{"text": "c"})
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "evt_000003" {
		t.Fatalf("id after recovery = %s, want evt_000003", ev.ID)
	}
	events, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("replayed %d events, want 3", len(events))
	}
}

func TestMissingNewlineHealed(t *testing.T) {
	path := tmpEvents(t)
	w, _ := Open(path)
	w.Append(MsgUser, map[string]string{"text": "a"})
	w.Close()

	data, _ := os.ReadFile(path)
	os.WriteFile(path, data[:len(data)-1], 0o644)

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if _, err := w2.Append(Note, map[string]string{"text": "b"}); err != nil {
		t.Fatal(err)
	}
	events, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].ID != "evt_000002" {
		t.Fatalf("healed replay = %+v", events)
	}
}

func TestReplayFold(t *testing.T) {
	path := tmpEvents(t)
	w, _ := Open(path)
	defer w.Close()
	w.Append(MsgUser, map[string]string{"text": "q"})
	w.Append(MsgAssistant, map[string]string{"text": "a"})
	w.Append(Plan, map[string]any{"steps": []string{"s1", "s2"}})
	w.Append(Checkpoint, map[string]string{"step": "s1", "status": "done"})

	events, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	st := Fold(events)
	if len(st.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(st.Messages))
	}
	if st.Plan == nil {
		t.Fatal("plan not folded")
	}
	if st.LastCheckpoint == nil || st.LastCheckpoint.ID != "evt_000004" {
		t.Fatalf("checkpoint = %+v", st.LastCheckpoint)
	}
	if st.Counts[MsgUser] != 1 || st.Counts[Checkpoint] != 1 {
		t.Fatalf("counts = %+v", st.Counts)
	}
}
