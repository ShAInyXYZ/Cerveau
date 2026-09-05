package loop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"cerveau/internal/episodic"
)

func TestRunControlIdentityVersionAndIdempotency(t *testing.T) {
	l, _, _ := auditFixture(t, auditPlan(), "http://unused.invalid")
	ctx, h, finish, err := l.beginRun(context.Background(), "s", "discussion", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if l.runs.get("s") == h {
			finish(nil, nil)
		}
	}()
	control := func(action, id string, version uint64) (*RunState, error) {
		return l.Control("s", action, RunControl{RunID: h.state.ID, ID: id, Version: version})
	}
	bad := RunControl{RunID: "previous-run", ID: "stale-steer", Text: "do not record this"}
	if _, err := l.Control("s", "steer", bad); !errors.Is(err, ErrControlConflict) {
		t.Fatalf("stale steer: %v", err)
	}
	paused, err := control("pause", "pause-1", 0)
	if err != nil || paused.ControlVersion != 1 || paused.Status != "pause_requested" {
		t.Fatalf("pause: %+v, %v", paused, err)
	}
	if _, err := control("pause", "pause-1", 0); err != nil {
		t.Fatalf("duplicate pause: %v", err)
	}
	resumed, err := control("resume", "resume-1", 1)
	if err != nil || resumed.ControlVersion != 2 || h.paused.Load() {
		t.Fatalf("resume: %+v, %v", resumed, err)
	}
	if st, err := control("pause", "pause-1", 0); err != nil || st.ControlVersion != 2 || h.paused.Load() {
		t.Fatalf("late retry undid resume: %+v, %v", st, err)
	}
	if _, err := control("pause", "delayed-new-pause", 0); !errors.Is(err, ErrControlConflict) {
		t.Fatalf("stale version: %v", err)
	}
	if _, err := control("kill", "pause-1", 2); !errors.Is(err, ErrControlConflict) {
		t.Fatalf("conflicting id reuse: %v", err)
	}
	if _, err := control("kill", "kill-1", 2); err != nil || ctx.Err() == nil {
		t.Fatalf("kill did not cancel: %v", err)
	}
	// A tool/question's deferred completion must not undo cancellation.
	SetWaiting(ctx, false)
	if st := l.RunStateOf("s"); st.Status != "cancelling" {
		t.Fatalf("completion overwrote cancellation: %+v", st)
	}
	finish(nil, nil)
	_, newer, finishNewer, err := l.beginRun(context.Background(), "s", "discussion", "")
	if err != nil {
		t.Fatal(err)
	}
	defer finishNewer(nil, nil)
	if st, err := control("kill", "kill-1", 2); err != nil || st.ID != h.state.ID || st.Status != "cancelled" {
		t.Fatalf("terminal retry: %+v, %v", st, err)
	}
	if _, err := control("pause", "old-run-new-control", 3); !errors.Is(err, ErrControlConflict) {
		t.Fatalf("old run control accepted: %v", err)
	}
	if newer.killed.Load() || newer.paused.Load() || newer.state.ControlVersion != 0 {
		t.Fatalf("old control affected newer run: %+v", newer.state)
	}
	events, _ := episodic.Replay(l.path("s"))
	for _, ev := range events {
		if ev.Type == episodic.MsgUser {
			var p struct {
				Text string `json:"text"`
			}
			json.Unmarshal(ev.Payload, &p)
			if p.Text == bad.Text {
				t.Fatal("stale steer was journaled")
			}
		}
	}
}

func TestRunControlPauseKeepsOwnership(t *testing.T) {
	l, _, _ := auditFixture(t, auditPlan(), "http://unused.invalid")
	ctx, h, finish, err := l.beginRun(context.Background(), "s", "discussion", "")
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil, nil)
	if _, err := l.Control("s", "pause", RunControl{RunID: h.state.ID, ID: "pause"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.boundary(ctx) }()
	deadline := time.Now().Add(time.Second)
	for l.RunStateOf("s").Status != "paused" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if l.RunStateOf("s").Status != "paused" {
		t.Fatal("did not reach paused boundary")
	}
	if _, _, _, err := l.beginRun(context.Background(), "s", "discussion", "other"); !errors.Is(err, ErrBusy) {
		t.Fatalf("paused ownership lost: %v", err)
	}
	if _, _, _, err := l.beginRun(context.Background(), "other-session", "discussion", "other"); !errors.Is(err, ErrBusy) {
		t.Fatalf("paused workspace lease lost: %v", err)
	}
	select {
	case <-done:
		t.Fatal("paused worker returned")
	default:
	}
	if _, err := l.Control("s", "resume", RunControl{RunID: h.state.ID, ID: "resume", Version: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("resume did not wake owner")
	}
}

func TestRunRecoveryRecordsInterruptedBeforeReplacement(t *testing.T) {
	l, wr, _ := auditFixture(t, auditPlan(), "http://unused.invalid")
	auditAppend(t, wr, episodic.RunState, RunState{ID: "vanished", Status: "waiting_user", Phase: "ask_user"})
	if st := l.RunStateOf("s"); st.Status != "interrupted" {
		t.Fatalf("unowned run: %+v", st)
	}
	_, current, finish, err := l.beginRun(context.Background(), "s", "discussion", "")
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil, nil)
	events, _ := episodic.Replay(l.path("s"))
	recovered, accepted := -1, -1
	for i, ev := range events {
		if ev.Type != episodic.RunState {
			continue
		}
		var st RunState
		json.Unmarshal(ev.Payload, &st)
		if st.ID == "vanished" && st.Status == "interrupted" {
			recovered = i
		}
		if st.ID == current.state.ID {
			accepted = i
		}
	}
	if recovered < 0 || accepted <= recovered {
		t.Fatalf("recovery=%d acceptance=%d", recovered, accepted)
	}
	if err := l.WithRun("s", "vanished", func() error { t.Fatal("delivered to vanished owner"); return nil }); !errors.Is(err, ErrControlConflict) {
		t.Fatalf("old answer owner: %v", err)
	}
}
