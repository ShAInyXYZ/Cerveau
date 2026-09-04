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
				// Index is written by the step supervisor. Matching on TITLE
				// alone puts one checkpoint on every step that happens to share
				// a name, and loses which revision it came from.
				Index *int `json:"index"`
			}
			if json.Unmarshal(ev.Payload, &cp) != nil {
				continue
			}
			summary := cp.Summary
			if summary == "" {
				summary = cp.Detail
			}
			if cp.Index != nil {
				if i := *cp.Index; i >= 0 && i < len(plan.Steps) {
					stepStatus[i] = StepReport{
						Title: plan.Steps[i].Title, Status: cp.Status, Summary: summary,
						TS: ev.TS.Format("15:04:05"),
					}
				}
				continue
			}
			for idx, ps := range plan.Steps {
				if ps.Title == cp.Step {
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
		// Disk reconciliation: a pending or partial step whose declared files
		// all exist really is done — the checkpoint just never got written
		// (chat-mode turns write no step checkpoints at all).
		//
		// ONLY when the step's files are its own. A plan that appends to one
		// file — the common shape for a single-page build — gives every step
		// the same `files: [index.html]`, so the moment step 1 writes it,
		// EVERY step reconciles to done, including a final "Verify" step that
		// never ran. The car run reported 4/4 green while the turn was dying
		// in a check_page loop (2026-09-04). Existence proves a file was
		// written; it cannot prove which step wrote it, and it can never prove
		// a verification passed. When steps share files, only a real
		// checkpoint counts.
		shared := false
		for other, op := range plan.Steps {
			if other != idx && sameFiles(op.Files, ps.Files) {
				shared = true
				break
			}
		}
		if (sr.Status == "pending" || sr.Status == "partial") && workspace != "" && len(ps.Files) > 0 && !shared {
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
func statOK(p string) bool { _, err := os.Stat(p); return err == nil }

// sameFiles reports whether two steps declare the same file set, in any order.
// Two steps that touch the same files cannot be told apart by looking at disk,
// so neither may be reconciled to "done" from existence alone.
func sameFiles(a, b []string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, f := range a {
		seen[filepath.Clean(f)]++
	}
	for _, f := range b {
		k := filepath.Clean(f)
		if seen[k] == 0 {
			return false
		}
		seen[k]--
	}
	return true
}

func filesPresent(workspace string, paths []string) int {
	root, err := filepath.Abs(workspace)
	if err != nil {
		root = workspace
	}
	n := 0
	// the project often lives one directory below the workspace (ws/game/js/x.js
	// while the plan says js/x.js) — try the direct join, then one level down
	subdirs, _ := os.ReadDir(root)
	for _, p := range paths {
		full := filepath.Join(root, filepath.Clean("/"+p))
		if _, err := os.Stat(full); err != nil {
			for _, sd := range subdirs {
				if sd.IsDir() {
					if alt := filepath.Join(root, sd.Name(), filepath.Clean("/"+p)); statOK(alt) {
						full = alt
						break
					}
				}
			}
		}
		if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
			continue
		}
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			n++
		}
	}
	return n
}
