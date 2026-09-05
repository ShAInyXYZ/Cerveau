package loop

import (
	"cerveau/internal/episodic"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestCommandAdmissionDedupAndOwnership(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	l, path := setupInterruptLoop(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{textReply("done")}})
	})
	cmd := Command{ID: "once", Kind: "chat", Mode: "discussion", Text: "hello"}
	st, err := l.Start("s1", cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	duplicate, err := l.Start("s1", cmd, nil)
	if err != nil || duplicate.ID != st.ID {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	if _, err := l.Run(context.Background(), "s1", "overlap", "discussion"); !errors.Is(err, ErrBusy) {
		t.Fatalf("overlap allowed: %v", err)
	}
	changed := cmd
	changed.Text = "different"
	if _, err := l.Start("s1", changed, nil); err == nil {
		t.Fatal("key reused with different request")
	}
	// Settings for an accepted run cannot change under its feet.
	l.SetThinking("always", "xhigh")
	l.SetSampling("creative")
	active := l.RunStateOf("s1")
	if active.ThinkingMode != st.ThinkingMode || active.Sampling != st.Sampling {
		t.Fatal("active settings changed")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for len(l.RunningSessions()) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	terminal := l.RunStateOf("s1")
	if terminal.Status != "completed" {
		t.Fatalf("terminal: %+v", terminal)
	}
	again, err := l.Start("s1", cmd, nil)
	if err != nil || again.ID != st.ID || again.Status != "completed" {
		t.Fatalf("terminal dedup %+v %v", again, err)
	}
	events, _ := episodic.Replay(path)
	users := 0
	for _, e := range events {
		if e.Type == episodic.MsgUser {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("duplicate user events %d", users)
	}
}

func TestSessionWorkspaceLeaseAcrossLoopInstances(t *testing.T) {
	l, _, done := stepRunFixture(t)
	defer done()
	ctx, _, finish, err := l.beginRun(context.Background(), "s1", "autopilot", "")
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil, nil)
	other := New(l.llm, l.registry(), l.open, l.path, nil)
	other.SetWorkspaceFunc(l.workspace)
	if _, err := other.Run(ctxWithoutOwner(ctx), "s2", "build", "autopilot"); !errors.Is(err, ErrBusy) {
		t.Fatalf("cross-process lease was not enforced: %v", err)
	}
}
func ctxWithoutOwner(context.Context) context.Context { return context.Background() }

func TestOutOfOrderStepRejected(t *testing.T) {
	l, _, done := stepRunFixture(t)
	defer done()
	if _, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: 1}); err == nil {
		t.Fatal("skipped unfinished dependency")
	}
}
