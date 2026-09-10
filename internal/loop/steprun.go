package loop

import (
	"cerveau/internal/episodic"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cerveau/internal/plan"
)

type StepRunRequest struct {
	Step     int    `json:"step"`
	Revision bool   `json:"revision"`
	PlanID   string `json:"plan_event_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Continue bool   `json:"continue_plan,omitempty"`
}
type PlanState struct {
	PlanRevision   string      `json:"plan_revision,omitempty"`
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
// steps are not revisions. Shared-file evidence may become needs_reverify;
// checks outside the selection require a subsequent, explicitly wider run.
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
	st := &PlanState{Title: s.Plan.Title, PlanID: id, PlanRevision: s.Plan.Revision, Steps: s.Steps, Next: s.Next(), Blocked: s.Blocked(), Done: s.Done(), Reverify: s.reverify, RevisionTarget: s.revisionTarget}
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
	var planScope struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(events[start].Payload, &planScope)
	var reviewOwner reviewReplayOwner
	s := NewSupervisor(&p)
	// Amendment metadata is reducer-owned; a raw plan cannot self-authorize it.
	p.Revision, p.Guidance, p.Amendments = "", nil, nil
	for offset, ev := range events[start+1:] {
		if ev.Type == episodic.RunState {
			var owner reviewReplayOwner
			if json.Unmarshal(ev.Payload, &owner) == nil && owner.ID != "" {
				reviewOwner = owner
			}
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
			if json.Unmarshal(ev.Payload, &st) != nil || st.PlanID != id || st.PlanRevision != p.Revision || len(st.Steps) != len(s.Steps) {
				continue
			}
			validIDs := true
			for i, step := range st.Steps {
				if step.ID != plan.StepID(p.Steps[i].ID, i) || step.Index != i {
					validIDs = false
					break
				}
			}
			if !validIDs {
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
		if ev.Type == episodic.Note {
			projectPlanAmendment(s, id, planScope.SessionID, reviewOwner, events[start:start+1+offset], ev)
			// A review note is durable before the normal blocked PlanState.
			// Project it at its journal position so a later authoritative
			// PlanState supersedes it, while an interruption cannot erase it.
			projectVerificationReviewNote(s, id, planScope.SessionID, reviewOwner, events[start+1:start+1+offset], ev)
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

type reviewReplayOwner struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Step      int    `json:"step"`
}

// This projects an already-persisted handoff, never approval or a replacement
// check. The journal remains the trust boundary; explicit foreign/stale scope,
// changed contracts, and unbound current results fail closed.
func projectVerificationReviewNote(s *Supervisor, planID, planSession string, owner reviewReplayOwner, prior []episodic.Event, ev episodic.Event) {
	var n struct {
		Kind      string              `json:"kind"`
		Index     *int                `json:"index"`
		PlanID    string              `json:"plan_event_id"`
		SessionID string              `json:"session_id"`
		RunID     string              `json:"run_id"`
		Review    *VerificationReview `json:"review"`
		Verdict   *Verdict            `json:"verdict"`
	}
	if json.Unmarshal(ev.Payload, &n) != nil || n.Kind != "verification_review_requested" || n.Index == nil || *n.Index < 0 || *n.Index >= len(s.Steps) || n.Review == nil || n.Verdict == nil {
		return
	}
	i, review, verdict := *n.Index, n.Review, n.Verdict
	if n.PlanID != planID || review.PlanID != planID || review.Index != i || n.SessionID == "" || review.SessionID != n.SessionID || n.RunID == "" || review.RunID != n.RunID || (planSession != "" && planSession != n.SessionID) {
		return
	}
	if owner.ID != "" && (owner.ID != n.RunID || (owner.RunID != "" && owner.RunID != n.RunID) || (owner.SessionID != "" && owner.SessionID != n.SessionID) || owner.Step != i || terminalRun(owner.Status)) {
		return
	}
	v := s.Plan.Steps[i].Verify
	if v == nil || v.Validate() != nil || review.OriginalVerify == nil || !sameReviewJSON(v, review.OriginalVerify) {
		return
	}
	original, _ := json.Marshal(v)
	if review.OriginalCheckSHA256 != recoverySHA(original) || review.Status != "human_review_required" || strings.TrimSpace(review.Reason) == "" || (review.EventID != "" && review.EventID != ev.ID) {
		return
	}
	identity := *review
	identity.ProposalID, identity.EventID = "", ""
	raw, _ := json.Marshal(identity)
	if review.ProposalID != "verification_review_"+recoverySHA(raw) {
		return
	}
	if verdict.VerificationReview != nil || verdict.Check != v.Describe() || verdict.EvidenceEventID == "" || review.CurrentEvidenceEventID != verdict.EvidenceEventID || review.CurrentWorkspaceVersion != verdict.WorkspaceVersion || review.CurrentCheckPass != verdict.Pass {
		return
	}
	if !reviewMatchesLatestCheck(prior, planID, n.SessionID, n.RunID, i, v, *verdict) {
		return
	}
	st := &s.Steps[i]
	if st.Verdict != nil && st.Verdict.VerificationReview != nil && st.Verdict.VerificationReview.ProposalID == review.ProposalID {
		return // duplicate note must not count another attempt
	}
	review.EventID = ev.ID
	verdict.VerificationReview = review
	st.Status, st.Verdict = "blocked", verdict
	st.Attempts++
	st.Reason = "Verification review requested: " + review.Reason
}

// A fresh result is the latest completed check for this run/step, paired with
// its exact verify_started contract. In particular, an old passing receipt or
// a newer, different result cannot be used to manufacture this handoff.
func reviewMatchesLatestCheck(events []episodic.Event, planID, sessionID, runID string, idx int, v *plan.Verify, current Verdict) bool {
	finished := false
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != episodic.Note {
			continue
		}
		var n struct {
			Kind      string
			Index     *int
			Verify    *plan.Verify
			Verdict   *Verdict
			SessionID string `json:"session_id"`
			RunID     string `json:"run_id"`
			PlanID    string `json:"plan_event_id"`
		}
		if json.Unmarshal(events[i].Payload, &n) != nil || n.SessionID != sessionID || n.RunID != runID || (n.PlanID != "" && n.PlanID != planID) || n.Index == nil || *n.Index != idx {
			continue
		}
		switch n.Kind {
		case "verify_finished":
			if finished || n.Verdict == nil {
				return false
			}
			if strings.EqualFold(strings.TrimSpace(v.Kind), "contains") && n.Verdict.EvidenceEventID == "" {
				n.Verdict.EvidenceEventID = events[i].ID
			}
			if !sameReviewJSON(n.Verdict, current) {
				return false
			}
			finished = true
		case "verify_started":
			return finished && sameReviewJSON(n.Verify, v)
		}
	}
	return false
}

func sameReviewJSON(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(x) == string(y)
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
	unlock := plan.LockMutation(sid)
	defer unlock()
	id, err := requireCurrentPlan(l.path(sid), s.Plan)
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
	// Admission must precede retry/reopen state writes. A rejected contract
	// must not consume or clear attempts merely because Recover was clicked.
	if err := validatePlanDelivery(l.path(sid), p, sid); err != nil {
		return nil, err
	}
	i := req.Step
	if i < -1 {
		return nil, fmt.Errorf("step must be -1 or a nonnegative index")
	}
	if i < 0 {
		i = s.Next()
		if req.Continue {
			i = recoveryTarget(s)
		}
	}
	if i < 0 || i >= len(p.Steps) {
		return nil, fmt.Errorf("no runnable step; completed or blocked plan")
	}
	for j := 0; j < i; j++ {
		repairing := recoveryInstructions(s, i) != ""
		if s.Steps[j].Status != "passed" && !(repairing && s.Steps[j].Status == "needs_reverify") {
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
	return l.runPlanFrom(ctx, sid, p, s, i, !req.Continue, req.Reason)
}
