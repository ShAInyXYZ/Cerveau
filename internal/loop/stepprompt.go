package loop

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"cerveau/internal/plan"
)

// The prompt a step's run actually receives.
//
// The old design injected the USER'S original prompt into every iteration of
// one long run and trusted the model to touch each planned point on its own.
// That is what made the plan decoration: nothing tied a run to a step, so
// nothing could verify a step.
//
// Every step retains the original user constraints beside this narrower frame:
// what to build, in which files, and the exact check it must satisfy. The check
// is quoted verbatim. A generated plan never supersedes the original task.
//
// StepPrompt builds the frame; the model fills in the judgement. It is
// deliberately assembled from committed facts rather than generated prose, so
// the prompt cannot drift from the plan it claims to execute. What the model
// contributes is CONTEXT — see WithContext.

// StepPrompt is the instruction for one step's run.
type StepPrompt struct {
	Index           int
	Rev             int
	Step            PlanStep
	Verify          *plan.Verify
	Context         string // related steps' recorded evidence, or why this is a revision
	Sources         string // what the model read while planning; sent as its own item
	RecoveryCanSkip bool   // unchanged failed-check retry without a revision/steer
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
		// Describe is a clipped report label, not an executable contract. A
		// fresh step/retry window needs every argument, including assertions
		// beyond the first 120 characters. Verify contains only string fields,
		// so it is always JSON-serializable.
		contract, _ := json.MarshalIndent(v, "", "  ")
		b.WriteString("Committed verification (exact JSON):\n```json\n" + string(contract) + "\n```\n\n")
		b.WriteString("The harness runs this check automatically when you stop; a pass covers " +
			"only what it actually checks. Do not weaken it.\n\n")
	}
	b.WriteString("The original user constraints remain binding. A model-generated criterion " +
		"can conflict with those constraints or with another step's committed behavior. " +
		"Use read_plan_step with a zero-based index to retrieve the exact related step and check; " +
		"context summaries are not executable contracts. If you find a conflict, use " +
		"request_verification_review with the conflicting criterion and evidence, and stop for review. " +
		"Do not change fixtures or metrics just to satisfy arbitrary counts, silently relax an " +
		"assertion, or treat your proposed replacement as approved. Preserve related checks " +
		"unless the operator explicitly approves a change.\n\n")
	b.WriteString("Implement and verify all behavior in this step's title and detail. " +
		"Run focused checks for behavior the committed check does not exercise; do not defer " +
		"that proof to later integration steps. Report unavailable or unperformed checks as " +
		"unverified, never passed.\n\n")

	b.WriteString("Do this step ONLY. Later steps have their own runs; work that belongs to " +
		"them is not wanted here. If you find that an EARLIER step is missing something you " +
		"need, say so plainly — name the step number and what it must add — and stop rather " +
		"than patching around it here.\n\n" +
		"When the step's work and checks are finished, stop and report the observations " +
		"and any remaining limitations in one or two sentences.")

	return b.String()
}

const maxStepContextBytes = 6000

// StepContext retains recorded passing evidence from related steps, including
// downstream checks and checks invalidated by shared-file work. Stale evidence
// is useful context, never a current pass. Exact contracts are available through
// read_plan_step; the bounded summaries here are deliberately not executable.
func StepContext(sup *Supervisor) string {
	if sup == nil {
		return ""
	}
	var rows []string
	for i := range sup.Steps {
		st := &sup.Steps[i]
		if (st.Status != "passed" && st.Status != "needs_reverify") || st.Verdict == nil || !st.Verdict.Pass {
			continue
		}
		status := "passed at this snapshot (check result only)"
		if st.Status == "needs_reverify" {
			status = "previously passed; awaiting recheck (needs_reverify; not current verification)"
		}
		files := "not recorded"
		if sup.Plan != nil && i < len(sup.Plan.Steps) && len(sup.Plan.Steps[i].Files) > 0 {
			files = strings.Join(sup.Plan.Steps[i].Files, ", ")
		}
		row := fmt.Sprintf("  %d. %s — %s\n    Files summary: %s\n    Check summary: %s\n    Evidence summary: %s\n",
			i+1, stepContextExcerpt(st.Title, 160), status, stepContextExcerpt(files, 320),
			stepContextExcerpt(st.Verdict.Check, 300), stepContextExcerpt(st.Verdict.Evidence, 240))
		if st.Reason != "" {
			row += "    Reason summary: " + stepContextExcerpt(st.Reason, 160) + "\n"
		}
		if st.Verdict.EvidenceEventID != "" {
			row += "    Evidence ref: " + stepContextExcerpt(st.Verdict.EvidenceEventID, 120) + "\n"
		}
		row += fmt.Sprintf("    Exact step/check and full recorded verdict: read_plan_step {\"index\":%d}\n", i)
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return ""
	}
	const header = "Related steps that passed their declared checks (not proof of untested behavior):\n"
	const footer = "Preserve related checks and behavior, including previously passed shared-file steps awaiting recheck. " +
		"This is a bounded context summary; omitted text is not an exact check. " +
		"Use read_plan_step with the zero-based index (step number minus one) for any exact contract or full recorded evidence."
	var b strings.Builder
	b.WriteString(header)
	for i, row := range rows {
		// Leave space for the omission count and the exact-read instructions.
		if b.Len()+len(row)+len(footer)+100 > maxStepContextBytes {
			fmt.Fprintf(&b, "  %d additional step summaries omitted to keep context bounded.\n", len(rows)-i)
			break
		}
		b.WriteString(row)
	}
	b.WriteString(footer)
	return b.String()
}

func stepContextExcerpt(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	const omitted = " [omitted]"
	n := limit - len(omitted)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + omitted
}

// RevisionContext explains WHY a step was reopened, in the asker's words.
func RevisionContext(askerIdx int, askerTitle, reason string) string {
	r := strings.TrimSpace(reason)
	if r == "" {
		r = "it needs something this step did not provide"
	}
	return fmt.Sprintf("Step %d (%s) reported: %q", askerIdx+1, askerTitle, r)
}
