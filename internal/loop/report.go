package loop

import (
	"encoding/json"

	"cerveau/internal/episodic"
)

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

func BuildReport(events []episodic.Event) *Report {
	planIdx := -1
	var plan Plan
	var planEvtID string
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.Plan {
			if err := json.Unmarshal(events[i].Payload, &plan); err == nil {
				planIdx = i
				planEvtID = events[i].ID
			}
			break
		}
	}
	if planIdx < 0 {
		return nil
	}
	rep := &Report{Title: plan.Title, PlanEventID: planEvtID}
	stepStatus := map[int]StepReport{}
	for i := planIdx + 1; i < len(events); i++ {
		ev := events[i]
		switch ev.Type {
		case episodic.Checkpoint:
			var cp struct {
				Step    string `json:"step"`
				Status  string `json:"status"`
				Summary string `json:"summary"`
				Detail  string `json:"detail"`
			}
			if json.Unmarshal(ev.Payload, &cp) != nil {
				continue
			}
			for idx, ps := range plan.Steps {
				if ps.Title == cp.Step {
					summary := cp.Summary
					if summary == "" {
						summary = cp.Detail
					}
					stepStatus[idx] = StepReport{
						Title: cp.Step, Status: cp.Status, Summary: summary,
						TS: ev.TS.Format("15:04:05"),
					}
				}
			}
		case episodic.TurnClose:
			var tc struct {
				Autopilot bool `json:"autopilot"`
				Handback  bool `json:"handback"`
			}
			if json.Unmarshal(ev.Payload, &tc) == nil && tc.Autopilot {
				rep.Handback = tc.Handback
				rep.FinishedAt = ev.TS.Format("15:04:05")
			}
		}
	}
	for idx, ps := range plan.Steps {
		sr, ok := stepStatus[idx]
		if !ok {
			sr = StepReport{Title: ps.Title, Status: "pending"}
		}
		rep.Steps = append(rep.Steps, sr)
		switch sr.Status {
		case "done":
			rep.Done++
		case "failed":
			rep.Failed++
		case "skipped":
			rep.Skipped++
		}
	}
	return rep
}
