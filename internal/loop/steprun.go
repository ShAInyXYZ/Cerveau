package loop

import (
	"cerveau/internal/episodic"
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

type StepRunRequest struct {
	Step     int    `json:"step"`
	Revision bool   `json:"revision"`
	PlanID   string `json:"plan_event_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
}
type PlanState struct {
	Title          string      `json:"title"`
	PlanID         string      `json:"plan_event_id"`
	Steps          []StepState `json:"steps"`
	Next           int         `json:"next"`
	Blocked        int         `json:"blocked"`
	Done           bool        `json:"done"`
	Pairs          []pairCount `json:"pair_failures,omitempty"`
	Reverify       []int       `json:"reverify,omitempty"`
	RevisionTarget int         `json:"revision_target"`
}
type pairCount struct {
	Target int `json:"target"`
	Asker  int `json:"asker"`
	Count  int `json:"count"`
}

// A selected command is one server-owned run, not a browser queue. Plans are
// sequential: an unfinished predecessor must be selected too. Already-passed
// steps are not revisions; they stay passed unless a selected step explicitly
// requests a correction through the supervisor.
type selectionKey struct{}
type stepSelection []int

func (s stepSelection) includes(i int) bool {
	j := sort.SearchInts(s, i)
	return j < len(s) && s[j] == i
}

func (s stepSelection) next(sup *Supervisor) int {
	for _, i := range s {
		if sup.Steps[i].Status == "blocked" {
			return -1
		}
		if sup.Steps[i].Status != "passed" {
			return i
		}
	}
	return -1
}

func (s stepSelection) done(sup *Supervisor) bool {
	for _, i := range s {
		if sup.Steps[i].Status != "passed" {
			return false
		}
	}
	return true
}

func validateSelection(sup *Supervisor, steps []int) error {
	if len(steps) == 0 {
		return fmt.Errorf("select at least one step")
	}
	scope := stepSelection(steps)
	for n, i := range scope {
		if i < 0 || i >= len(sup.Steps) {
			return fmt.Errorf("invalid selected step index %d", i)
		}
		if n > 0 && i <= scope[n-1] {
			return fmt.Errorf("selected steps must be unique and in ascending order")
		}
	}
	for _, i := range scope {
		if sup.Steps[i].Status == "blocked" {
			return fmt.Errorf("step %d is blocked; retry or revise that step explicitly first", i+1)
		}
		for j := 0; j < i; j++ {
			if sup.Steps[j].Status != "passed" && !scope.includes(j) {
				return fmt.Errorf("selected step %d depends on unfinished step %d; include it in the selection", i+1, j+1)
			}
		}
	}
	return nil
}

func (l *Loop) RunSelected(ctx context.Context, sid, planID string, steps []int) (result *Result, runErr error) {
	ctx, _, finish, err := l.beginRun(ctx, sid, "autopilot", "")
	if err != nil {
		return nil, err
	}
	defer func() { finish(result, runErr) }()
	events, err := episodic.Replay(l.path(sid))
	if err != nil {
		return nil, err
	}
	sup, id, err := ReducePlan(events)
	if err != nil {
		return nil, err
	}
	if planID == "" || planID != id {
		return nil, fmt.Errorf("plan changed or plan_event_id missing; refresh before running selected steps")
	}
	if err := validateSelection(sup, steps); err != nil {
		return nil, err
	}
	scope := stepSelection(append([]int(nil), steps...))
	ctx = context.WithValue(ctx, selectionKey{}, scope)
	return l.runPlanFrom(ctx, sid, sup.Plan, sup, scope.next(sup), false, "")
}

func supervisorState(s *Supervisor, id string) *PlanState {
	st := &PlanState{Title: s.Plan.Title, PlanID: id, Steps: s.Steps, Next: s.Next(), Blocked: s.Blocked(), Done: s.Done(), Reverify: s.reverify, RevisionTarget: s.revisionTarget}
	for pair, n := range s.pairFails {
		st.Pairs = append(st.Pairs, pairCount{pair[0], pair[1], n})
	}
	sort.Slice(st.Pairs, func(i, j int) bool {
		a, b := st.Pairs[i], st.Pairs[j]
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Asker < b.Asker
	})
	return st
}

// ReducePlan is shared by execution recovery and every UI projection.
func ReducePlan(events []episodic.Event) (*Supervisor, string, error) {
	start := -1
	var p Plan
	id := ""
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.Plan {
			if err := json.Unmarshal(events[i].Payload, &p); err != nil {
				return nil, "", err
			}
			start = i
			id = events[i].ID
			break
		}
	}
	if start < 0 {
		return nil, "", fmt.Errorf("no committed plan in this session")
	}
	s := NewSupervisor(&p)
	for _, ev := range events[start+1:] {
		if ev.Type == episodic.RunState {
			var run RunState
			if json.Unmarshal(ev.Payload, &run) == nil && (run.Status == "interrupted" || run.Status == "cancelled" || run.Status == "failed") {
				for i := range s.Steps {
					if s.Steps[i].Status == "running" || s.Steps[i].Status == "verifying" {
						s.Steps[i].Status = "pending"
						s.Steps[i].Reason = "Execution interrupted; inspect existing effects before retrying."
					}
				}
			}
			continue
		}
		if ev.Type == episodic.PlanState {
			var st PlanState
			if json.Unmarshal(ev.Payload, &st) != nil || st.PlanID != id || len(st.Steps) != len(s.Steps) {
				continue
			}
			s.Steps = st.Steps
			s.reverify = st.Reverify
			s.revisionTarget = st.RevisionTarget
			s.pairFails = map[[2]int]int{}
			for _, pair := range st.Pairs {
				s.pairFails[[2]int{pair.Target, pair.Asker}] = pair.Count
			}
			continue
		}
		if ev.Type != episodic.Checkpoint {
			continue
		}
		var cp struct {
			ProjectionOnly                                           bool   `json:"projection_only"`
			Index                                                    *int   `json:"index"`
			PlanID                                                   string `json:"plan_event_id"`
			Status, Step, Summary, Detail, Check, Evidence, Decision string
			Rev                                                      int
		}
		if json.Unmarshal(ev.Payload, &cp) != nil || cp.ProjectionOnly || (cp.PlanID != "" && cp.PlanID != id) {
			continue
		}
		i := -1
		if cp.Index != nil {
			i = *cp.Index
		} else {
			for j, st := range s.Steps {
				if st.Title == cp.Step {
					if i >= 0 {
						i = -1
						break
					}
					i = j
				}
			}
		}
		if i < 0 || i >= len(s.Steps) {
			continue
		}
		st := &s.Steps[i]
		st.Rev = cp.Rev
		evidence := cp.Evidence
		if evidence == "" {
			evidence = cp.Summary
		}
		if evidence == "" {
			evidence = cp.Detail
		}
		switch cp.Status {
		case "done", "passed":
			st.Status = "passed"
			if p.Steps[i].Verify == nil {
				st.Status = "unverified"
			}
			st.Attempts++
			st.Verdict = &Verdict{Pass: st.Status == "passed", Check: cp.Check, Evidence: evidence}
		case "failed", "blocked":
			st.Attempts++
			st.Status = "failed"
			if cp.Decision == "blocked" || cp.Status == "blocked" || st.Attempts >= 2 {
				st.Status = "blocked"
			}
			st.Verdict = &Verdict{Check: cp.Check, Evidence: evidence}
		default:
			st.Status = "pending"
		}
	}
	return s, id, nil
}
func (l *Loop) PlanStateOf(sid string) (*PlanState, error) {
	projection, err := l.Snapshot(sid)
	if err != nil {
		return nil, err
	}
	if projection.Plan == nil {
		return nil, fmt.Errorf("no valid committed plan in this session")
	}
	return projection.Plan, nil
}
func (l *Loop) restoreSupervisor(sid string, _ *Plan) (*Supervisor, error) {
	events, err := episodic.Replay(l.path(sid))
	if err != nil {
		return nil, err
	}
	s, _, err := ReducePlan(events)
	return s, err
}
func (l *Loop) saveSupervisor(wr *episodic.Writer, sid string, s *Supervisor) error {
	_, id, err := LatestPlan(l.path(sid))
	if err != nil {
		return err
	}
	_, err = wr.Append(episodic.PlanState, supervisorState(s, id))
	return err
}
func (l *Loop) RunStep(ctx context.Context, sid string, req StepRunRequest) (result *Result, runErr error) {
	ctx, h, finish, err := l.beginRun(ctx, sid, "autopilot", req.Reason)
	if err != nil {
		return nil, err
	}
	defer func() { finish(result, runErr) }()
	p, id, err := LatestPlan(l.path(sid))
	if err != nil {
		return nil, err
	}
	if req.PlanID != "" && req.PlanID != id {
		return nil, fmt.Errorf("plan changed; refresh before running a step")
	}
	s, err := l.restoreSupervisor(sid, p)
	if err != nil {
		return nil, err
	}
	i := req.Step
	if i < -1 {
		return nil, fmt.Errorf("step must be -1 or a nonnegative index")
	}
	if i < 0 {
		i = s.Next()
	}
	if i < 0 || i >= len(p.Steps) {
		return nil, fmt.Errorf("no runnable step; completed or blocked plan")
	}
	for j := 0; j < i; j++ {
		if s.Steps[j].Status != "passed" {
			return nil, fmt.Errorf("step %d depends on unfinished step %d", i+1, j+1)
		}
	}
	if req.Revision || s.Steps[i].Status == "passed" {
		if err := s.Reopen(i, req.Reason); err != nil {
			return nil, err
		}
	} else if s.Steps[i].Status == "blocked" {
		s.Steps[i].Status = "pending"
		s.Steps[i].Attempts = 0
	}
	if err := l.saveSupervisor(h.writer, sid, s); err != nil {
		return nil, err
	}
	return l.runPlanFrom(ctx, sid, p, s, i, true, req.Reason)
}
