package loop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
)

func selectedPlan() *Plan {
	return &Plan{Title: "selected execution", Steps: []PlanStep{
		{Title: "foundation", Files: []string{"one"}, Verify: &plan.Verify{Kind: "contains", File: "one", Symbol: "READY"}},
		{Title: "dependent", Files: []string{"two"}, Verify: &plan.Verify{Kind: "contains", File: "two", Symbol: "READY"}},
		{Title: "not selected", Files: []string{"three"}, Verify: &plan.Verify{Kind: "contains", File: "three", Symbol: "READY"}},
	}}
}

func TestSelectionAdmissionRejectsInvalidOrIncompleteScope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		steps   []int
		passed  []int
		blocked int
		want    string
	}{
		{"empty", nil, nil, -1, "at least one"},
		{"negative", []int{-1}, nil, -1, "invalid"},
		{"out of range", []int{3}, nil, -1, "invalid"},
		{"duplicate", []int{0, 0}, nil, -1, "unique"},
		{"unordered", []int{1, 0}, nil, -1, "ascending"},
		{"missing first", []int{1}, nil, -1, "unfinished step 1"},
		{"mixed gap", []int{0, 2}, nil, -1, "unfinished step 2"},
		{"blocked requires explicit retry", []int{0, 1}, nil, 0, "blocked"},
		{"valid dependent chain", []int{0, 1}, nil, -1, ""},
		{"passed predecessor", []int{1}, []int{0}, -1, ""},
		{"passed gap", []int{0, 2}, []int{1}, -1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSupervisor(selectedPlan())
			for _, i := range tc.passed {
				s.Steps[i].Status = "passed"
			}
			if tc.blocked >= 0 {
				s.Steps[tc.blocked].Status = "blocked"
			}
			err := validateSelection(s, tc.steps)
			if tc.want == "" && err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSelectedCommandRequiresPlanIdentityAndRejectsMixedRequest(t *testing.T) {
	l, _, _ := auditFixture(t, selectedPlan(), "http://unused.invalid")
	_, planID, _ := LatestPlan(l.path("s"))
	for _, cmd := range []Command{
		{ID: "missing-plan", Kind: "selected", Steps: []int{0}},
		{ID: "stale-plan", Kind: "selected", Steps: []int{0}, PlanID: "stale"},
		{ID: "mixed-revision", Kind: "selected", Steps: []int{0}, PlanID: planID, Revision: true},
		{ID: "mixed-kind", Kind: "continue", Steps: []int{0}, PlanID: planID},
	} {
		if _, err := l.Start("s", cmd, nil); err == nil {
			t.Fatalf("accepted invalid command %+v", cmd)
		}
	}
	events, _ := episodic.Replay(l.path("s"))
	for _, ev := range events {
		if ev.Type == episodic.RunState {
			t.Fatal("invalid scope created a run")
		}
	}
}

func TestSelectedCommandContinuesWithoutBrowserAndKeepsOneRun(t *testing.T) {
	m := newScriptedModel(textReply("step complete"))
	defer m.srv.Close()
	l, _, ws := auditFixture(t, selectedPlan(), m.srv.URL)
	auditWrite(t, filepath.Join(ws, "one"), "READY")
	auditWrite(t, filepath.Join(ws, "two"), "READY")
	_, planID, _ := LatestPlan(l.path("s"))
	released := make(chan struct{})
	cmd := Command{ID: "selection-1", Kind: "selected", PlanID: planID, Steps: []int{0, 1}}
	accepted, err := l.Start("s", cmd, func() { close(released) })
	if err != nil {
		t.Fatal(err)
	}
	// No follow-up step request, browser observer or polling is needed for work
	// to advance. Caller-owned input may be discarded or reused after admission.
	cmd.Steps[1] = 2
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("selected worker did not complete independently")
	}
	st, err := l.PlanStateOf("s")
	if err != nil {
		t.Fatal(err)
	}
	if st.Steps[0].Status != "passed" || st.Steps[1].Status != "passed" || st.Steps[2].Status != "pending" {
		t.Fatalf("selection leaked or did not progress: %+v", st.Steps)
	}
	if run := l.RunStateOf("s"); run.ID != accepted.ID || run.Status != "completed" {
		t.Fatalf("run = %+v", run)
	}
	events, _ := episodic.Replay(l.path("s"))
	for _, ev := range events {
		if ev.Type != episodic.RunState {
			continue
		}
		var run RunState
		if err := json.Unmarshal(ev.Payload, &run); err != nil {
			t.Fatal(err)
		}
		if run.ID != accepted.ID {
			t.Fatalf("selection used multiple leases: %s and %s", accepted.ID, run.ID)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bodies) != 2 {
		t.Fatalf("model calls = %d, want only two selected steps", len(m.bodies))
	}
}

func TestSelectedFailureNeverAdvancesToDependent(t *testing.T) {
	m := newScriptedModel(textReply("done without evidence"))
	defer m.srv.Close()
	l, _, _ := auditFixture(t, selectedPlan(), m.srv.URL)
	_, planID, _ := LatestPlan(l.path("s"))
	result, err := l.RunSelected(context.Background(), "s", planID, []int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := l.PlanStateOf("s")
	if result.StopReason != "plan_blocked" || st.Steps[0].Status != "blocked" || st.Steps[1].Attempts != 0 {
		t.Fatalf("failure advanced dependent work: result=%+v steps=%+v", result, st.Steps)
	}
	if st.Steps[0].Attempts != 2 {
		t.Fatalf("attempt budget = %d, want 2", st.Steps[0].Attempts)
	}
}

func TestSelectedRevisionOutsideScopeInvalidatesButNeverExecutesTarget(t *testing.T) {
	m := newScriptedModel(toolCall("request_revision", `{"target":0,"reason":"foundation is incorrect"}`))
	defer m.srv.Close()
	l, wr, _ := auditFixture(t, selectedPlan(), m.srv.URL)
	auditAppend(t, wr, episodic.Checkpoint, map[string]any{"index": 0, "status": "done"})
	_, planID, _ := LatestPlan(l.path("s"))
	result, err := l.RunSelected(context.Background(), "s", planID, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := l.PlanStateOf("s")
	if result.StopReason != "plan_blocked" || st.Steps[0].Status != "pending" || st.Steps[0].Rev != 1 || st.Steps[0].Attempts != 0 {
		t.Fatalf("out-of-scope revision not handed back safely: result=%+v steps=%+v", result, st.Steps)
	}
	if !strings.Contains(st.Steps[1].Reason, "unselected step 1") {
		t.Fatalf("missing recovery reason: %+v", st.Steps[1])
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bodies) != 1 {
		t.Fatalf("executed out-of-scope target: %d calls", len(m.bodies))
	}
}

func TestSelectedRevisionInsideScopeLeavesUnselectedReverificationPending(t *testing.T) {
	m := newScriptedModel(
		toolCall("request_revision", `{"target":0,"reason":"foundation must be updated"}`),
		toolCall("write", `{"path":"one","content":"READY"}`),
		textReply("foundation corrected"), textReply("dependent done"),
	)
	defer m.srv.Close()
	l, wr, ws := auditFixture(t, selectedPlan(), m.srv.URL)
	auditWrite(t, filepath.Join(ws, "one"), "OLD")
	auditWrite(t, filepath.Join(ws, "two"), "READY")
	for _, i := range []int{0, 2} {
		auditAppend(t, wr, episodic.Checkpoint, map[string]any{"index": i, "status": "done"})
	}
	_, planID, _ := LatestPlan(l.path("s"))
	result, err := l.RunSelected(context.Background(), "s", planID, []int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := l.PlanStateOf("s")
	if result.StopReason != StopFinalAnswer || st.Steps[0].Status != "passed" || st.Steps[1].Status != "passed" || st.Steps[2].Status != "needs_reverify" {
		t.Fatalf("selection revised or verified outside scope: result=%+v steps=%+v", result, st.Steps)
	}
	if len(st.Reverify) != 1 || st.Reverify[0] != 2 {
		t.Fatalf("lost deferred reverification: %+v", st)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bodies) != 4 {
		t.Fatalf("model calls = %d, want 4", len(m.bodies))
	}
}
