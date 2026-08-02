package loop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cerveau/internal/episodic"
)

func TestBuildReport(t *testing.T) {
	ts := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	mk := func(id string, typ episodic.EventType, payload string) episodic.Event {
		return episodic.Event{ID: id, TS: ts, Type: typ, Payload: json.RawMessage(payload)}
	}
	events := []episodic.Event{
		mk("evt_000001", episodic.Plan, `{"title":"Ship it","steps":[{"title":"a"},{"title":"b"},{"title":"c"}],"autonomy_budget":"low"}`),
		mk("evt_000002", episodic.Checkpoint, `{"step":"a","status":"done","summary":"all good"}`),
		mk("evt_000003", episodic.Checkpoint, `{"step":"b","status":"failed","detail":"boom"}`),
		mk("evt_000004", episodic.TurnClose, `{"autopilot":true,"handback":true}`),
	}
	rep := BuildReport(events)
	if rep == nil {
		t.Fatal("no report built")
	}
	if rep.Title != "Ship it" || rep.PlanEventID != "evt_000001" {
		t.Fatalf("rep = %+v", rep)
	}
	if len(rep.Steps) != 3 {
		t.Fatalf("steps = %+v", rep.Steps)
	}
	if rep.Steps[0].Status != "done" || rep.Steps[0].Summary != "all good" {
		t.Fatalf("step a = %+v", rep.Steps[0])
	}
	if rep.Steps[1].Status != "failed" || rep.Steps[1].Summary != "boom" {
		t.Fatalf("step b = %+v", rep.Steps[1])
	}
	if rep.Steps[2].Status != "pending" {
		t.Fatalf("step c = %+v", rep.Steps[2])
	}
	if rep.Done != 1 || rep.Failed != 1 || rep.Skipped != 0 {
		t.Fatalf("counts = %d/%d/%d", rep.Done, rep.Failed, rep.Skipped)
	}
	if !rep.Handback {
		t.Fatal("handback not detected")
	}
}

func TestBuildReportNoPlan(t *testing.T) {
	if BuildReport(nil) != nil {
		t.Fatal("expected nil report without plan")
	}
}

// A step whose declared files all exist is DONE even when no checkpoint was
// ever written (the turn was cut short by a guard). Without this the chat's
// plan strip shows "pending" for work that is visibly finished on disk.
func TestReportReconcilesWithDisk(t *testing.T) {
	ws := t.TempDir()
	os.MkdirAll(filepath.Join(ws, "js"), 0o755)
	os.WriteFile(filepath.Join(ws, "js", "a.js"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(ws, "js", "b.js"), []byte("y"), 0o644)

	plan := Plan{Title: "P", Steps: []PlanStep{
		{Title: "one", Files: []string{"js/a.js", "js/b.js"}},
		{Title: "two", Files: []string{"js/c.js"}},
		{Title: "three"},
	}}
	raw, _ := json.Marshal(plan)
	events := []episodic.Event{{ID: "evt_1", Type: episodic.Plan, Payload: raw}}

	rep := BuildReportAt(events, ws)
	if rep == nil {
		t.Fatal("no report")
	}
	if rep.Steps[0].Status != "done" {
		t.Fatalf("step with all files present should be done, got %q", rep.Steps[0].Status)
	}
	if rep.Steps[1].Status == "done" {
		t.Fatalf("step with missing files must not be done, got %q", rep.Steps[1].Status)
	}
	if rep.Done != 1 {
		t.Fatalf("done count = %d, want 1", rep.Done)
	}
}
