package loop

import (
	"bytes"
	"cerveau/internal/plan"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// planStepReader holds serialized copies of the committed plan and its state at
// step entry. No mutable Plan/Verify/Files/Verdict pointers escape construction,
// and reads cannot observe later supervisor mutations. It neither reads the
// workspace nor runs a check; a historical pass remains historical evidence.
type planStepReader struct {
	steps []string
}

func newPlanStepReader(planID string, p *Plan, states []StepState) (*planStepReader, error) {
	if p == nil || len(p.Steps) == 0 {
		return nil, fmt.Errorf("read_plan_step requires a nonempty committed plan snapshot")
	}
	if len(p.Steps) != len(states) {
		return nil, fmt.Errorf("read_plan_step plan/state snapshot lengths differ")
	}
	r := &planStepReader{steps: make([]string, len(p.Steps))}
	for i, step := range p.Steps {
		// Serialization is also the deep copy: all strings, file slices, the
		// verification object and the verdict become privately owned bytes.
		out, err := json.Marshal(struct {
			Revision      string                 `json:"plan_revision"`
			Guidance      string                 `json:"implementation_guidance,omitempty"`
			Delivery      *plan.DeliveryContract `json:"delivery_contract,omitempty"`
			PlanID        string                 `json:"plan_event_id"`
			PlanTitle     string                 `json:"plan_title"`
			Autonomy      string                 `json:"plan_autonomy_budget"`
			Index         int                    `json:"index"`
			StepNumber    int                    `json:"step_number"`
			Snapshot      bool                   `json:"snapshot"`
			EvidenceScope string                 `json:"evidence_scope"`
			Step          PlanStep               `json:"step"`
			State         StepState              `json:"state"`
		}{
			Revision: effectivePlanRevision(planID, p), Guidance: p.Guidance[plan.StepID(step.ID, i)], Delivery: p.Delivery,
			PlanID: planID, PlanTitle: p.Title, Autonomy: p.AutonomyBudget,
			Index: i, StepNumber: i + 1, Snapshot: true,
			EvidenceScope: "Snapshot only. A needs_reverify verdict records a previous check, not current verification. Reading this tool does not run or approve a check.",
			Step:          step, State: states[i],
		})
		if err != nil {
			return nil, fmt.Errorf("copy plan step %d: %w", i+1, err)
		}
		r.steps[i] = string(out)
	}
	return r, nil
}

func (r *planStepReader) Name() string { return "read_plan_step" }

func (r *planStepReader) Description() string {
	return "Read an exact committed plan step and its status/reason/verdict snapshot, including the full verification check and evidence. index is zero-based (step number minus one). Read-only: does not execute, amend, approve or verify anything. needs_reverify means a previous pass is awaiting recheck, not a current pass."
}

func (r *planStepReader) Schema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"index": map[string]any{
			"type": "integer", "minimum": 0, "maximum": len(r.steps) - 1,
			"description": "Zero-based step index in this immutable plan snapshot (displayed step number minus one).",
		}},
		"required": []string{"index"},
	}
}

func (r *planStepReader) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Index *int `json:"index"`
	}
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&a); err != nil {
		return "", fmt.Errorf("read_plan_step arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("read_plan_step expects exactly one JSON argument object")
	}
	if a.Index == nil || *a.Index < 0 || *a.Index >= len(r.steps) {
		return "", fmt.Errorf("read_plan_step requires index in 0..%d (zero-based)", len(r.steps)-1)
	}
	return r.steps[*a.Index], nil
}
