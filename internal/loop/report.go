package loop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

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

// BuildReport keeps the event-only view (callers without a workspace).
func BuildReport(events []episodic.Event) *Report { return BuildReportAt(events, "") }

// BuildReportAt derives step status from checkpoints AND, when a workspace
// is given, from the filesystem. Checkpoints are only written when a step
// COMPLETES, so a step cut short by a guard leaves real files and no event —
// the chat's plan strip would show "pending" for work that is plainly done.
// Same reconciliation the planner panel does, applied at the source so both
// surfaces agree.
func BuildReportAt(events []episodic.Event, workspace string) *Report {
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
		// Disk reconciliation: a pending step whose declared files all exist
		// really is done — the checkpoint just never got written.
		if sr.Status == "pending" && workspace != "" && len(ps.Files) > 0 {
			if have := filesPresent(workspace, ps.Files); have == len(ps.Files) {
				sr.Status = "done"
				sr.Summary = "verified on disk"
			} else if have > 0 {
				sr.Status = "partial"
			}
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

// filesPresent counts how many workspace-relative paths exist. Containment
// mirrors the file-tool jail: a path escaping the workspace never counts.
func filesPresent(workspace string, paths []string) int {
	root, err := filepath.Abs(workspace)
	if err != nil {
		root = workspace
	}
	n := 0
	for _, p := range paths {
		full := filepath.Join(root, filepath.Clean("/"+p))
		if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
			continue
		}
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			n++
		}
	}
	return n
}
