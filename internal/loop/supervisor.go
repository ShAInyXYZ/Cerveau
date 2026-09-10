package loop

import (
	"cerveau/internal/plan"
	"fmt"
	"strings"
)

// The step supervisor owns the cursor: which step runs next, at which revision,
// and what happens to the verdict that comes back.
//
// WHY IT EXISTS. Until now a plan was appended to the system prompt as advisory
// text ("Follow this plan's intent") and one run carried the original prompt,
// trusted to touch every step on its own. Nothing bound a run to a step, so
// nothing verified a step, no checkpoint was ever written, and the panel had to
// guess progress from which files existed. The plan was decoration.
//
// Here the plan is the work queue. One run per step, verified against that
// step's own declared check, and only a pass advances the cursor.
//
// The supervisor decides; it never writes code. It starts runs and reads
// verdicts, which keeps every rule below in one readable place.

// maxRevisions is how many times one step may be reopened.
//
// Two, then hand back. A later step can discover that an earlier one is
// incomplete — step 3 needs a variable step 1 should have declared — and
// reopening it is correct. But two steps that disagree about the same file can
// trade revisions forever, each honestly reporting that it needs the other
// changed. A third request is not new information, it is a design disagreement,
// and that is the user's call.
const maxRevisions = 2

// StepState is one step's standing: what happened, how many times, and why.
type StepState struct {
	ID       string   `json:"id"`
	Index    int      `json:"index"`
	Title    string   `json:"title"`
	Status   string   `json:"status"` // pending | running | passed | failed | blocked
	Rev      int      `json:"rev"`    // 0 = first attempt
	Verdict  *Verdict `json:"verdict,omitempty"`
	Attempts int      `json:"attempts"`
	Reason   string   `json:"reason,omitempty"`
}

// Supervisor tracks a plan's execution.
type Supervisor struct {
	Plan  *Plan
	Steps []StepState

	// pairFails counts how often the SAME (revised, downstream) pair has
	// failed. A pair that fails twice is a genuine disagreement, not a fixable
	// slip, so it hands back instead of revising a third time.
	pairFails      map[[2]int]int
	reverify       []int
	revisionTarget int
}

func NewSupervisor(p *Plan) *Supervisor {
	s := &Supervisor{Plan: p, pairFails: map[[2]int]int{}, revisionTarget: -1}
	for i, st := range p.Steps {
		s.Steps = append(s.Steps, StepState{ID: plan.StepID(st.ID, i), Index: i, Title: st.Title, Status: "pending"})
	}
	return s
}

// Next is the step to run now, or -1 when there is nothing left to do.
// Steps run in order; a blocked step stops the queue rather than being skipped,
// because later steps were planned to build on it.
func (s *Supervisor) Next() int {
	for i := range s.Steps {
		switch s.Steps[i].Status {
		case "passed":
			continue
		case "blocked":
			return -1
		default:
			return i
		}
	}
	return -1
}

// Done reports whether every step passed.
func (s *Supervisor) Done() bool {
	for i := range s.Steps {
		if s.Steps[i].Status != "passed" {
			return false
		}
	}
	return true
}

// Blocked reports the first step that gave up, or -1.
func (s *Supervisor) Blocked() int {
	for i := range s.Steps {
		if s.Steps[i].Status == "blocked" {
			return i
		}
	}
	return -1
}

// Record applies a verdict to a step and returns what to do next.
//
// needsStep is the index of an EARLIER step the run says is incomplete, or -1.
// The model asks for that; the harness never guesses it.
type Decision struct {
	Action    string // advance | retry | revise | blocked | done
	Step      int    // the step the action refers to
	Rev       int    // revision number when revising
	Reverify  []int  // downstream steps to re-check after a revision
	HandBack  bool   // stop and let the user decide
	Reasoning string // one line, for the checkpoint and the UI
}

func (s *Supervisor) Record(idx int, v Verdict, needsStep int) Decision {
	if idx < 0 || idx >= len(s.Steps) {
		return Decision{Action: "blocked", Step: idx, HandBack: true, Reasoning: "no such step"}
	}
	st := &s.Steps[idx]
	st.Attempts++
	st.Verdict = &v
	if v.VerificationReview != nil {
		// A disputed criterion is not an implementation retry or an approval.
		// Keep the actual check result, including a pass, but stop advancement.
		st.Status = "blocked"
		st.Reason = "Verification review requested: " + v.VerificationReview.Reason
		return Decision{Action: "blocked", Step: idx, HandBack: true, Reasoning: st.Reason}
	}

	// A run that asks to reopen an EARLIER step takes priority over its own
	// verdict: it cannot honestly pass while its foundation is wrong.
	if needsStep >= 0 && needsStep < idx {
		st.Status = "pending"
		return s.revise(needsStep, idx)
	}

	if v.Pass {
		st.Status = "passed"
		st.Reason = ""
		if idx == s.revisionTarget && len(s.reverify) > 0 {
			return Decision{Action: "reverify", Step: idx, Reverify: append([]int(nil), s.reverify...), Reasoning: "target passed; recheck invalidated downstream evidence now"}
		}
		if s.Done() {
			return Decision{Action: "done", Step: idx, Reasoning: "every step passed its own check"}
		}
		return Decision{Action: "advance", Step: idx, Reasoning: "passed: " + v.Check}
	}

	// Failed its own check. One retry, then stop — an impossible criterion
	// (the car run's eval could never pass under file://) must cost one step,
	// not the whole task.
	st.Status = "failed"
	if st.Attempts >= 2 {
		st.Status = "blocked"
		return Decision{Action: "blocked", Step: idx, HandBack: true,
			Reasoning: fmt.Sprintf("failed its check twice — %s. Either the step is wrong or the check is impossible; both need you", v.Check)}
	}
	return Decision{Action: "retry", Step: idx, Reasoning: "failed: " + v.Check}
}

// revise reopens an earlier step, under the two rules that stop ping-pong.
func (s *Supervisor) revise(target, asker int) Decision {
	tgt := &s.Steps[target]

	// Rule 1: a cap. Two revisions, then the user decides.
	if tgt.Rev >= maxRevisions {
		tgt.Status = "blocked"
		return Decision{Action: "blocked", Step: target, HandBack: true,
			Reasoning: fmt.Sprintf("step %d has been revised %d times and step %d still needs it changed — that is a design disagreement, not a slip",
				target+1, tgt.Rev, asker+1)}
	}

	// Rule 2: the same pair failing twice is a standoff, not progress.
	key := [2]int{target, asker}
	s.pairFails[key]++
	if s.pairFails[key] > 1 {
		tgt.Status = "blocked"
		return Decision{Action: "blocked", Step: target, HandBack: true,
			Reasoning: fmt.Sprintf("steps %d and %d disagree about the same work twice over — handing back with both verdicts",
				target+1, asker+1)}
	}

	if err := s.Reopen(target, fmt.Sprintf("step %d requested a correction", asker+1)); err != nil {
		return Decision{Action: "blocked", Step: target, HandBack: true, Reasoning: err.Error()}
	}

	// A revision can invalidate a later step that was verified against the OLD
	// file. Re-run the declared checks of every later PASSED step that shares a
	// file with the revised one — cheap, because it is the check and not a new
	// run, and it catches the silent breakage.
	return Decision{
		Action: "revise", Step: target, Rev: tgt.Rev,
		// Invalidated now; verification is scheduled only AFTER target pass.
		Reasoning: fmt.Sprintf("step %d needs step %d changed first", asker+1, target+1),
	}
}

func (s *Supervisor) Reopen(target int, reason string) error {
	if target < 0 || target >= len(s.Steps) {
		return fmt.Errorf("no such revision target")
	}
	tgt := &s.Steps[target]
	if tgt.Rev >= maxRevisions {
		tgt.Status = "blocked"
		return fmt.Errorf("step %d reached its revision limit; revise the plan explicitly", target+1)
	}
	tgt.Rev++
	tgt.Status = "pending"
	tgt.Attempts = 0
	tgt.Reason = reason
	s.revisionTarget = target
	s.reverify = nil
	for i := target + 1; i < len(s.Steps); i++ {
		if s.Steps[i].Status == "passed" || s.Steps[i].Status == "needs_reverify" {
			s.Steps[i].Status = "needs_reverify"
			s.reverify = append(s.reverify, i)
		}
	}
	return nil
}

// downstreamSharing lists later steps that already passed and touch a file the
// revised step touches.
func (s *Supervisor) downstreamSharing(target int) []int {
	if target < 0 || target >= len(s.Plan.Steps) {
		return nil
	}
	touched := map[string]bool{}
	for _, f := range s.Plan.Steps[target].Files {
		touched[strings.TrimSpace(f)] = true
	}
	var out []int
	for i := target + 1; i < len(s.Steps); i++ {
		if s.Steps[i].Status != "passed" {
			continue
		}
		for _, f := range s.Plan.Steps[i].Files {
			if touched[strings.TrimSpace(f)] {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// ReverifyFailed marks a downstream step whose re-check failed after a revision.
// It goes back in the queue rather than blocking: its foundation just moved, so
// one honest re-run is owed to it.
func (s *Supervisor) ReverifyFailed(idx int, v Verdict) {
	if idx < 0 || idx >= len(s.Steps) {
		return
	}
	s.Steps[idx].Status = "pending"
	s.Steps[idx].Attempts = 0
	s.Steps[idx].Verdict = &v
}
