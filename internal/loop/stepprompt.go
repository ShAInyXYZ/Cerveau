package loop

import (
	"fmt"
	"strings"

	"cerveau/internal/plan"
)

// The prompt a step's run actually receives.
//
// The old design injected the USER'S original prompt into every iteration of
// one long run and trusted the model to touch each planned point on its own.
// That is what made the plan decoration: nothing tied a run to a step, so
// nothing could verify a step.
//
// Here the user's prompt is used once — to plan. From then on each run gets a
// prompt about ONE step: what to build, in which files, and the exact check it
// must satisfy. The check is quoted verbatim, because a step that does not know
// how it will be judged will be judged anyway.
//
// StepPrompt builds the frame; the model fills in the judgement. It is
// deliberately assembled from committed facts rather than generated prose, so
// the prompt cannot drift from the plan it claims to execute. What the model
// contributes is CONTEXT — see WithContext.

// StepPrompt is the instruction for one step's run.
type StepPrompt struct {
	Index   int
	Rev     int
	Step    PlanStep
	Verify  *plan.Verify
	Context string // what earlier steps produced, or why this is a revision
	Sources string // what the model read while planning; sent as its own item
}

// Text renders the prompt sent to the model.
func (p StepPrompt) Text() string {
	var b strings.Builder

	if p.Rev > 0 {
		fmt.Fprintf(&b, "REVISION %d of step %d: %s\n\n", p.Rev, p.Index+1, p.Step.Title)
		b.WriteString("A later step could not proceed because this step is incomplete. " +
			"Change ONLY what is needed to unblock it — do not rewrite work that already passed.\n\n")
	} else {
		fmt.Fprintf(&b, "STEP %d of the committed plan: %s\n\n", p.Index+1, p.Step.Title)
	}

	if p.Step.Detail != "" {
		b.WriteString(p.Step.Detail + "\n\n")
	}
	if len(p.Step.Files) > 0 {
		fmt.Fprintf(&b, "Files for this step: %s\n\n", strings.Join(p.Step.Files, ", "))
	}
	if p.Context != "" {
		b.WriteString(p.Context + "\n\n")
	}

	if v := p.Verify; v != nil {
		b.WriteString("This step is DONE when this check passes:\n  " + v.Describe() + "\n\n")
		b.WriteString("The check runs automatically when you stop. Make it pass — do not " +
			"report success without it, and do not weaken it.\n\n")
	}

	b.WriteString("Do this step ONLY. Later steps have their own runs; work that belongs to " +
		"them is not wanted here. If you find that an EARLIER step is missing something you " +
		"need, say so plainly — name the step number and what it must add — and stop rather " +
		"than patching around it here.\n\n" +
		"When the step's work is written, stop and report in one or two sentences.")

	return b.String()
}

// StepContext summarises what earlier steps produced, so a run knows the ground
// it is standing on. Facts only: which steps passed and what proved them. A
// generated summary here would be one more thing that can be wrong.
func StepContext(sup *Supervisor) string {
	var done []string
	for i := range sup.Steps {
		st := &sup.Steps[i]
		if st.Status != "passed" || st.Verdict == nil {
			continue
		}
		done = append(done, fmt.Sprintf("  %d. %s — verified: %s", i+1, st.Title, st.Verdict.Check))
	}
	if len(done) == 0 {
		return ""
	}
	return "Already done and verified:\n" + strings.Join(done, "\n")
}

// RevisionContext explains WHY a step was reopened, in the asker's words.
func RevisionContext(askerIdx int, askerTitle, reason string) string {
	r := strings.TrimSpace(reason)
	if r == "" {
		r = "it needs something this step did not provide"
	}
	return fmt.Sprintf("Step %d (%s) reported: %q", askerIdx+1, askerTitle, r)
}
