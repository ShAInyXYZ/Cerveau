package loop

import "cerveau/internal/episodic"

type StepReport struct {
	Title   string `json:"title"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	TS      string `json:"ts"`
}

type Report struct {
	Title       string       `json:"title"`
	PlanEventID string       `json:"plan_event_id"`
	Steps       []StepReport `json:"steps"`
	Done        int          `json:"done"`
	Failed      int          `json:"failed"`
	Skipped     int          `json:"skipped"`
	Handback    bool         `json:"handback"`
	FinishedAt  string       `json:"finished_at"`
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
		switch step.Status {
		case "passed":
			sr.Status = "done"
			rep.Done++
		case "blocked", "failed":
			rep.Failed++
		default:
			rep.Skipped++
		}
		rep.Steps = append(rep.Steps, sr)
	}
	return rep
}
