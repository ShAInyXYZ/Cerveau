package loop

import (
	"cerveau/internal/episodic"
	"context"
	"encoding/json"
	"testing"
)

func TestSnapshotCrashProjectionNeverManufacturesPass(t *testing.T) {
	l, _, done := stepRunFixture(t)
	path := l.path("s1")
	defer done()
	wr, err := l.open("s1")
	if err != nil {
		t.Fatal(err)
	}
	events, _ := episodic.Replay(path)
	sup, id, err := ReducePlan(events)
	if err != nil {
		t.Fatal(err)
	}
	sup.Steps[0].Status = "verifying"
	wr.Append(episodic.PlanState, supervisorState(sup, id))
	ev, _ := wr.Append(episodic.RunState, RunState{ID: "crashed", Status: "running", Phase: "verifying"})
	p, err := l.Snapshot("s1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Cursor != ev.ID || p.Run.Status != "interrupted" || p.Running || p.Plan.Steps[0].Status != "pending" || p.Report.Steps[0].Status != "pending" || p.Report.Done != 0 {
		t.Fatalf("inconsistent recovery: %+v %+v %+v", p, p.Plan, p.Report)
	}
	// A later append cannot change an already-returned snapshot.
	wr.Append(episodic.RunState, RunState{ID: "new", Status: "completed"})
	if p.Run.ID != "crashed" || p.Cursor != ev.ID {
		t.Fatal("snapshot mutated")
	}
}

func TestSnapshotExternalLeaseIsNotACrash(t *testing.T) {
	l, _, done := stepRunFixture(t)
	defer done()
	_, h, finish, err := l.beginRun(context.Background(), "s1", "autopilot", "")
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil, nil)
	other := New(l.llm, l.registry(), l.open, l.path, nil)
	p, err := other.Snapshot("s1")
	if err != nil || !p.Running || p.Run.ID != h.state.ID {
		t.Fatalf("live external owner marked interrupted: %+v %v", p, err)
	}
}

func TestSnapshotPrefixPlanAndReportAgree(t *testing.T) {
	l, _, done := stepRunFixture(t)
	path := l.path("s1")
	defer done()
	wr, _ := l.open("s1")
	events, _ := episodic.Replay(path)
	sup, id, _ := ReducePlan(events)
	sup.Steps[0].Status = "passed"
	sup.Steps[0].Verdict = &Verdict{Pass: true, Evidence: "checked"}
	wr.Append(episodic.PlanState, supervisorState(sup, id))
	sup.Steps[0].Status = "needs_reverify"
	wr.Append(episodic.PlanState, supervisorState(sup, id))
	events, _ = episodic.Replay(path)
	for i := 1; i <= len(events); i++ {
		p := ProjectEvents(events[:i], "")
		if p.Plan == nil {
			continue
		}
		passed := 0
		for _, step := range p.Plan.Steps {
			if step.Status == "passed" {
				passed++
			}
		}
		if p.Report.Done != passed || p.Report.PlanEventID != p.Plan.PlanID {
			raw, _ := json.Marshal(p)
			t.Fatalf("prefix %d disagrees: %s", i, raw)
		}
	}
}
