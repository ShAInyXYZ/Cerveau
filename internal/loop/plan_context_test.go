package loop

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"cerveau/internal/plan"
)

func planContextFixture() (*Plan, []StepState) {
	p := &Plan{Title: "Voxel world", AutonomyBudget: "bounded", Steps: []PlanStep{
		{Title: "Mesh", Detail: "Preserve face coverage.\nKeep the flat floor at y=1.", Files: []string{"world.js", "tests.mjs"}, Risk: "safe", Verify: &plan.Verify{Kind: "command", Command: "node --input-type=module -e \"" + strings.Repeat("/* complete check */", 100) + "assert.equal(feet, 1)\""}},
		{Title: "Browser", Files: []string{"index.html"}},
	}}
	states := []StepState{
		{ID: "step-1", Index: 0, Title: "Mesh", Status: "needs_reverify", Rev: 1, Attempts: 2, Reason: "shared source changed", Verdict: &Verdict{Pass: true, Check: "physics check", Evidence: strings.Repeat("exact evidence\n", 100), EvidenceEventID: "evt_000101", WorkspaceVersion: "sha-old"}},
		{ID: "step-2", Index: 1, Title: "Browser", Status: "pending"},
	}
	return p, states
}

func TestReadPlanStepReturnsExactContractAndSnapshotEvidence(t *testing.T) {
	p, states := planContextFixture()
	r, err := newPlanStepReader("evt_plan", p, states)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name() != "read_plan_step" || !strings.Contains(r.Description(), "snapshot") {
		t.Fatalf("reader identity/description=%q %q", r.Name(), r.Description())
	}
	out, err := r.Execute(context.Background(), json.RawMessage(`{"index":0}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		PlanID    string    `json:"plan_event_id"`
		PlanTitle string    `json:"plan_title"`
		Index     int       `json:"index"`
		Number    int       `json:"step_number"`
		Snapshot  bool      `json:"snapshot"`
		Step      PlanStep  `json:"step"`
		State     StepState `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.PlanID != "evt_plan" || got.PlanTitle != p.Title || got.Index != 0 || got.Number != 1 || !got.Snapshot {
		t.Fatalf("missing selected-plan identity: %+v", got)
	}
	if !reflect.DeepEqual(got.Step, p.Steps[0]) || !reflect.DeepEqual(got.State, states[0]) {
		t.Fatalf("reader changed/truncated the committed step or historical verdict:\n%s", out)
	}
	if strings.Contains(out, "verified\":true") {
		t.Fatal("a prior pass must not become a current verification")
	}
	last, err := r.Execute(context.Background(), json.RawMessage(`{"index":1}`))
	if err != nil || !strings.Contains(last, `"step_number":2`) || !strings.Contains(last, `"status":"pending"`) {
		t.Fatalf("last valid index/legacy missing check not readable: %s %v", last, err)
	}
}

func TestReadPlanStepClonesMutableInputs(t *testing.T) {
	p, states := planContextFixture()
	r, err := newPlanStepReader("evt_plan", p, states)
	if err != nil {
		t.Fatal(err)
	}
	before, err := r.Execute(context.Background(), json.RawMessage(`{"index":0}`))
	if err != nil {
		t.Fatal(err)
	}
	p.Title = "replacement plan"
	p.Steps[0].Title = "replacement step"
	p.Steps[0].Files[0] = "replacement.js"
	p.Steps[0].Verify.Command = "weakened check"
	states[0].Status, states[0].Reason = "passed", "changed reason"
	states[0].Verdict.Pass, states[0].Verdict.Evidence = false, "changed evidence"
	after, err := r.Execute(context.Background(), json.RawMessage(`{"index":0}`))
	if err != nil || before != after {
		t.Fatalf("immutable reader changed with caller's mutable inputs: %v\nbefore=%s\nafter=%s", err, before, after)
	}
}

func TestReadPlanStepRejectsInvalidIndicesAndArguments(t *testing.T) {
	p, states := planContextFixture()
	r, err := newPlanStepReader("evt_plan", p, states)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`{}`, `null`, `{"index":null}`, `{"index":-1}`, `{"index":2}`, `{"index":1.5}`, `{"index":"0"}`, `{"index":0,"verify":{"command":"true"}}`, `{"index":0} {"index":1}`} {
		t.Run(input, func(t *testing.T) {
			if got, err := r.Execute(context.Background(), json.RawMessage(input)); err == nil || got != "" {
				t.Fatalf("invalid or write-like arguments accepted: %s %v", got, err)
			}
		})
	}
	if _, err := newPlanStepReader("evt_plan", nil, nil); err == nil {
		t.Fatal("nil plan accepted")
	}
	if _, err := newPlanStepReader("evt_plan", p, states[:1]); err == nil {
		t.Fatal("mismatched plan/state snapshot accepted")
	}
}
