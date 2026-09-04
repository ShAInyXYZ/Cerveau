package loop

import (
	"context"
	"encoding/json"
	"fmt"

	"cerveau/internal/episodic"
)

// Driving a plan from outside: run ONE step, or ask what the plan's state is.
//
// Both surfaces need this. The RFX planner has always had per-step buttons,
// but they worked by composing an English sentence — "Continue the committed
// plan: do step 3 only, then stop and report" — and posting it as an ordinary
// autopilot turn. The core had no idea a step was requested, so nothing bound
// the run to step 3, nothing verified step 3, and no checkpoint was written.
// The panel then guessed status from which files existed.
//
// A surface that looks like it controls execution, sitting on a core with no
// concept of a step, is the whole decoration problem. These two calls give the
// core the semantics the buttons always implied.

// StepRunRequest asks for one step of the committed plan.
type StepRunRequest struct {
	// Step is 0-based. -1 means "whichever step is next", which is what a
	// Continue button wants.
	Step int `json:"step"`

	// Revision reopens a step that already passed, for the case a human can
	// see but the model did not report: this step is wrong, do it again with
	// what we now know.
	Revision bool `json:"revision"`
}

// PlanState is what a surface renders: the plan, and what is known about each
// step. Built from the same events the report uses, so the two never disagree.
type PlanState struct {
	Title   string      `json:"title"`
	PlanID  string      `json:"plan_event_id"`
	Steps   []StepState `json:"steps"`
	Next    int         `json:"next"`    // -1 when nothing is runnable
	Blocked int         `json:"blocked"` // -1 when nothing is blocked
	Done    bool        `json:"done"`
}

// PlanStateOf reads the committed plan and replays its checkpoints, so a
// surface can show real per-step status without running anything.
func (l *Loop) PlanStateOf(sessionID string) (*PlanState, error) {
	plan, planID, err := LatestPlan(l.path(sessionID))
	if err != nil {
		return nil, err
	}
	sup, err := l.restoreSupervisor(sessionID, plan)
	if err != nil {
		return nil, err
	}
	return &PlanState{
		Title: plan.Title, PlanID: planID, Steps: sup.Steps,
		Next: sup.Next(), Blocked: sup.Blocked(), Done: sup.Done(),
	}, nil
}

// restoreSupervisor rebuilds the cursor from the event log.
//
// The supervisor is not held in memory between requests on purpose: a panel
// button, a CLI call and a resumed session must all see the same state, and
// the log is the only thing all three share.
func (l *Loop) restoreSupervisor(sessionID string, plan *Plan) (*Supervisor, error) {
	sup := NewSupervisor(plan)
	events, err := episodic.Replay(l.path(sessionID))
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		if ev.Type != episodic.Checkpoint {
			continue
		}
		var cp struct {
			Index  *int   `json:"index"`
			Status string `json:"status"`
			Rev    int    `json:"rev"`
			Check  string `json:"check"`
			Sum    string `json:"summary"`
		}
		if json.Unmarshal(ev.Payload, &cp) != nil || cp.Index == nil {
			continue
		}
		i := *cp.Index
		if i < 0 || i >= len(sup.Steps) {
			continue
		}
		st := &sup.Steps[i]
		st.Rev = cp.Rev
		switch cp.Status {
		case "done":
			st.Status = "passed"
			st.Verdict = &Verdict{Pass: true, Check: cp.Check, Evidence: cp.Sum}
		case "failed":
			// A failure that was handed back reads as blocked; the surface
			// offers "run again" either way, so the distinction is cosmetic
			// here and the attempt count is what stops a loop.
			st.Status = "failed"
			st.Attempts = 1
			st.Verdict = &Verdict{Pass: false, Check: cp.Check, Evidence: cp.Sum}
		default:
			st.Status = "pending"
		}
	}
	return sup, nil
}

// RunStep executes exactly one step of the committed plan and verifies it.
//
// This is what a step button calls. It shares every rule with a full autopilot
// run — the step's own prompt, its declared check, a checkpoint carrying the
// verdict — because they are the same code path with a cursor of one.
func (l *Loop) RunStep(ctx context.Context, sessionID string, req StepRunRequest) (*Result, error) {
	plan, _, err := LatestPlan(l.path(sessionID))
	if err != nil {
		return nil, err
	}
	sup, err := l.restoreSupervisor(sessionID, plan)
	if err != nil {
		return nil, err
	}

	idx := req.Step
	if idx < 0 {
		idx = sup.Next()
		if idx < 0 {
			if sup.Done() {
				return nil, fmt.Errorf("every step has passed its check — nothing left to run")
			}
			return nil, fmt.Errorf("step %d is blocked; re-run it explicitly or revise the plan", sup.Blocked()+1)
		}
	}
	if idx >= len(plan.Steps) {
		return nil, fmt.Errorf("no step %d in this plan (it has %d)", idx+1, len(plan.Steps))
	}

	// Re-running a step that already passed is a revision: a human saw
	// something the check did not. Say so in the prompt, so the run knows it
	// is correcting rather than starting fresh.
	if req.Revision || sup.Steps[idx].Status == "passed" {
		sup.Steps[idx].Rev++
		sup.Steps[idx].Status = "pending"
		sup.Steps[idx].Attempts = 0
	}

	return l.runPlanFrom(ctx, sessionID, plan, sup, idx, true)
}
