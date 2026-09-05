package loop

import (
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
