package loop

import (
	"cerveau/internal/episodic"
	"strings"
)

type StepReport struct {
	Title   string `json:"title"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	TS      string `json:"ts"`
}

type Report struct {
	Title         string       `json:"title"`
	PlanEventID   string       `json:"plan_event_id"`
	Steps         []StepReport `json:"steps"`
	Done          int          `json:"done"`
	Failed        int          `json:"failed"`
	Skipped       int          `json:"skipped"`
	NeedsReverify int          `json:"needs_reverify"`
	Pending       int          `json:"pending"`
	Unverified    int          `json:"unverified"`
	Running       int          `json:"running"`
	Handback      bool         `json:"handback"`
	FinishedAt    string       `json:"finished_at"`
}

// Reports project the same reducer execution uses. File existence is never completion.
func BuildReport(events []episodic.Event) *Report { return BuildReportAt(events, "") }
func BuildReportAt(events []episodic.Event, _ string) *Report {
	s, id, err := ReducePlan(events)
	if err != nil {
		return nil
	}
	return reportFromSupervisor(s, id)
}

func reportFromSupervisor(s *Supervisor, id string) *Report {
	rep := &Report{Title: s.Plan.Title, PlanEventID: id, Handback: s.Blocked() >= 0}
	for _, step := range s.Steps {
		sr := StepReport{Title: step.Title, Status: step.Status, Summary: step.Reason}
		if step.Verdict != nil {
			sr.Summary = step.Verdict.Evidence
		}
		if step.Status == "needs_reverify" || (step.Verdict != nil && (step.Verdict.VerificationReview != nil || (step.Status != "passed" && step.Verdict.Pass))) {
			sr.Summary = stepSummary(step)
		}
		switch step.Status {
		case "passed":
			sr.Status = "done"
			rep.Done++
		case "blocked", "failed":
			rep.Failed++
		case "skipped":
			rep.Skipped++
		case "needs_reverify":
			rep.NeedsReverify++
		case "pending":
			rep.Pending++
		case "running", "verifying":
			rep.Running++
		default:
			rep.Unverified++
		}
		rep.Steps = append(rep.Steps, sr)
	}
	return rep
}

func planStatusLabel(status string) string {
	switch status {
	case "needs_reverify":
		return "awaiting recheck"
	case "pending":
		return "not started"
	case "done", "passed":
		return "verified"
	case "failed", "blocked":
		return "blocked"
	default:
		return strings.ReplaceAll(status, "_", " ")
	}
}

func stepSummary(st StepState) string {
	if st.Verdict != nil && st.Verdict.VerificationReview != nil {
		return verificationReviewSummary(*st.Verdict)
	}
	if st.Status == "needs_reverify" {
		if st.Verdict != nil && st.Verdict.Pass {
			return "Previously passed; awaiting recheck after shared-file work. Previous check: " + st.Verdict.Check
		}
		return "Awaiting recheck; no current passing evidence."
	}
	if st.Verdict != nil {
		if st.Verdict.Pass && st.Status != "passed" && st.Status != "done" {
			return "Previously passed; no current passing evidence while " + planStatusLabel(st.Status) + ". Previous check: " + st.Verdict.Check
		}
		return summaryFor(*st.Verdict, "")
	}
	return st.Reason
}

func verificationReviewSummary(v Verdict) string {
	r := v.VerificationReview
	result := "failed"
	if v.Pass {
		result = "passed"
	}
	text := "Verification review requested: " + recoveryExcerpt(r.Reason, 1200) +
		"\nOriginal committed check " + result + ": " + v.Check +
		"\nLatest observation: " + clipEvidence(v.Evidence)
	if v.EvidenceEventID != "" {
		text += "\nFull check evidence: " + v.EvidenceEventID
	}
	text += "\nReview proposal: " + r.ProposalID
	if r.EventID != "" {
		text += " (" + r.EventID + ")"
	}
	return text + "\nHuman review required. Any proposed check is not applied or executed. The original acceptance requirements are unchanged; retry does not approve a replacement."
}
