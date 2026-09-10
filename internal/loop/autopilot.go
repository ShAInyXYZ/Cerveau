package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/memory"
	"cerveau/internal/plan"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

type PlanStep struct {
	ID          string   `json:"id,omitempty"`
	MilestoneID string   `json:"milestone_id,omitempty"`
	Title       string   `json:"title"`
	Detail      string   `json:"detail"`
	Files       []string `json:"files"`
	Risk        string   `json:"risk"`

	// Verify is the check that proves this step done. Legacy plans without a
	// valid check remain unverified; file existence is not completion evidence.
	Verify *plan.Verify `json:"verify,omitempty"`
}

type Plan struct {
	Title          string                 `json:"title"`
	Steps          []PlanStep             `json:"steps"`
	AutonomyBudget string                 `json:"autonomy_budget"`
	Delivery       *plan.DeliveryContract `json:"delivery_contract,omitempty"`
	Revision       string                 `json:"revision,omitempty"`
	Guidance       map[string]string      `json:"implementation_guidance,omitempty"`
	Amendments     []PlanAmendment        `json:"amendments,omitempty"`
}

// AsGuidance renders the plan as a guidance block for the autopilot system prompt.
// It is intent, not a rigid script — the agent adapts as needed.
func (p *Plan) AsGuidance() string {
	var b strings.Builder
	fmt.Fprintf(&b, "COMMITTED PLAN (guidance): %s\n", p.Title)
	for i, s := range p.Steps {
		fmt.Fprintf(&b, "%d. %s", i+1, s.Title)
		if s.Detail != "" {
			fmt.Fprintf(&b, " — %s", s.Detail)
		}
		if len(s.Files) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(s.Files, ", "))
		}
		b.WriteByte('\n')
	}
	b.WriteString("Follow this plan's intent; re-plan on the fly if a step proves wrong or blocked.")
	return b.String()
}

func LatestPlan(eventsPath string) (*Plan, string, error) {
	events, err := episodic.Replay(eventsPath)
	if err != nil {
		return nil, "", err
	}
	s, id, err := ReducePlan(events)
	if err != nil {
		return nil, "", err
	}
	return s.Plan, id, nil
}

type StepResult struct {
	Step    string `json:"step"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func (l *Loop) RunAutopilot(ctx context.Context, sessionID string) (result *Result, runErr error) {
	ctx, _, finish, err := l.beginRun(ctx, sessionID, "autopilot", "")
	if err != nil {
		return nil, err
	}
	defer func() { finish(result, runErr) }()
	plan, planEvt, err := LatestPlan(l.path(sessionID))
	if err != nil {
		return nil, err
	}
	wr, err := l.open(sessionID)
	if err != nil {
		return nil, err
	}
	wr.Append(episodic.Note, map[string]string{"text": fmt.Sprintf("autopilot started on %s (%d steps)", planEvt, len(plan.Steps))})

	// The supervisor owns the cursor. Steps are no longer a for-range over the
	// plan: a step that fails is retried, a step that needs earlier work
	// reopens it as a revision, and only a PASSED check advances. Which step
	// runs next is its decision, not the loop counter's.
	sup, err := l.restoreSupervisor(sessionID, plan)
	if err != nil {
		return nil, err
	}
	return l.runPlanFrom(ctx, sessionID, plan, sup, sup.Next(), false, "")
}

// runPlanFrom is the one execution path. Autopilot walks the whole plan;
// RunStep sets single to true and stops after one step. Sharing the body is
// deliberate: a step run by a button must obey exactly the same rules — its own
// prompt, its own check, a checkpoint carrying the verdict — as a step run by
// autopilot, or the two surfaces drift apart again.
func (l *Loop) runPlanFrom(ctx context.Context, sessionID string, plan *Plan, sup *Supervisor, start int, single bool, steer string) (result *Result, runErr error) {
	ctx, h, finish, err := l.beginRun(ctx, sessionID, "autopilot", steer)
	if err != nil {
		return nil, err
	}
	defer func() { finish(result, runErr) }()
	wr := h.writer
	if _, err := requireCurrentPlan(l.path(sessionID), plan); err != nil {
		return nil, err
	}
	original := taskBrief(l.path(sessionID))
	deliveryBoundary, err := planDeliveryAdmission(l.path(sessionID), plan, sessionID)
	if err != nil {
		return nil, err
	}
	if deliveryBoundary != "" {
		if _, err := wr.Append(episodic.Note, map[string]string{"kind": "legacy_delivery_order_unvalidated", "text": deliveryBoundary}); err != nil {
			return nil, err
		}
	}
	// The same effective snapshot is refreshed in place after a guidance
	// amendment; neither the supervisor nor the running recovery budgets restart.
	sup.Plan = plan
	_, notes, err := l.prepareRunRegistry(ctx, sessionID, original)
	if err != nil {
		return nil, err
	}
	systemPrompt := basePrompt + l.envBlock(sessionID) + "\n\n" + ReminderGuidance + "\n\n" + ModeByName("autopilot").Module
	for _, note := range notes {
		systemPrompt += "\n\n" + note
	}
	h.brief = runBrief(original, h.brief)
	var pulls []memory.Pull
	if l.recall != nil {
		pulls = l.recall.TurnStart(ctx, sessionID, plan.Title, nil)
	}
	first := start
	scope, selected := ctx.Value(selectionKey{}).(stepSelection)
	if selected {
		if _, err := wr.Append(episodic.Note, map[string]any{"kind": "selected_scope", "steps": scope, "text": "Server-owned selection; no unselected execution or verification is authorized."}); err != nil {
			return nil, err
		}
	}
	handback := false
	attempts := 0
	for ; attempts < 3*len(plan.Steps)+4; attempts++ {
		if err := h.boundary(ctx); err != nil {
			return &Result{StopReason: "cancelled"}, err
		}
		idx := first
		first = -1
		if idx < 0 {
			if selected {
				idx = scope.next(sup)
			} else {
				idx = sup.Next()
			}
		}
		if idx < 0 {
			handback = !sup.Done()
			if selected {
				handback = !scope.done(sup)
			}
			break
		}
		st := &sup.Steps[idx]
		if selected {
			// Revisions may invalidate dependencies after admission. Never
			// silently widen the selection to repair that changed foundation.
			for j := 0; j < idx; j++ {
				repairingSelected := sup.Steps[j].Status == "needs_reverify" && scope.includes(j) && st.Verdict != nil && !st.Verdict.Pass
				if sup.Steps[j].Status != "passed" && !repairingSelected {
					st.Reason = fmt.Sprintf("Selected execution paused: step %d needs unfinished step %d first.", idx+1, j+1)
					handback = true
					break
				}
			}
			if handback {
				break
			}
		}
		if err := plan.Steps[idx].Verify.Validate(); err != nil {
			st.Status = "blocked"
			st.Reason = "Plan needs a valid check before execution: " + err.Error()
			st.Verdict = &Verdict{Check: "unverified", Evidence: st.Reason}
			if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
				return nil, err
			}
			handback = true
			break
		}
		h.mu.Lock()
		h.state.Step = idx
		h.mu.Unlock()
		if st.Status == "needs_reverify" {
			// Reopening a saved run must not ask the model to rebuild work
			// merely because its evidence was invalidated before interruption.
			rv := l.verifyStep(ctx, wr, sessionID, idx, plan.Steps[idx].Verify)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			st.Verdict = &rv
			if rv.Pass {
				st.Status = "passed"
			} else {
				st.Status = "blocked"
				st.Reason = "Stale evidence recheck failed: " + rv.Evidence
				handback = true
			}
			if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
				return nil, err
			}
			if handback || single {
				break
			}
			continue
		}
		st.Status = "running"
		baseRegistry := h.registry
		attemptCtx, recoveryBrief, err := l.prepareRecovery(ctx, sessionID, plan, sup, idx)
		if err != nil {
			return nil, err
		}
		// A step may modify a shared source even if it subsequently crashes.
		// Invalidate the old evidence before model execution, not after success.
		stale := invalidateSharedEvidence(sup, idx)
		if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
			return nil, err
		}
		summary, stepErr := l.runStep(attemptCtx, wr, sessionID, systemPrompt, ModeByName("autopilot"), plan, idx, pulls,
			StepPrompt{Index: idx, Rev: st.Rev, Step: plan.Steps[idx], Verify: plan.Steps[idx].Verify,
				Context:         stepRunContext(sup, idx, steer) + "\n" + st.Reason + "\n" + recoveryBrief,
				RecoveryCanSkip: steer == "" && st.Rev == 0 && st.Verdict != nil && !st.Verdict.Pass && st.Verdict.VerificationReview == nil && st.Verdict.Check != "revision requested"})
		h.registry = baseRegistry
		var snapshotFailure *planSnapshotError
		if errors.As(stepErr, &snapshotFailure) {
			return nil, stepErr
		}
		if ctx.Err() != nil || h.killed.Load() {
			st.Status = "pending"
			_ = l.saveSupervisor(wr, sessionID, sup)
			return &Result{StopReason: "cancelled", Reply: "Cancelled; completed files are retained."}, ctx.Err()
		}
		var requested *revisionRequest
		var reviewRequested *verificationReviewRequest
		errors.As(stepErr, &reviewRequested)
		needs := -1
		if errors.As(stepErr, &requested) {
			needs = requested.target
			summary = requested.reason
		}
		var verdict Verdict
		if needs >= 0 {
			verdict = Verdict{Check: "revision requested", Evidence: summary}
		} else {
			st.Status = "verifying"
			if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
				return nil, err
			}
			if err := h.boundary(ctx); err != nil {
				return nil, err
			}
			verdict = l.verifyStep(attemptCtx, wr, sessionID, idx, plan.Steps[idx].Verify)
			if reviewRequested != nil {
				if err := persistVerificationReview(wr, reviewRequested, verdict); err != nil {
					return nil, err
				}
				verdict.VerificationReview = reviewRequested.Review
			} else {
				withExecutionStop(&verdict, stepErr)
			}
		}
		if ctx.Err() != nil {
			st.Status = "pending"
			_ = l.saveSupervisor(wr, sessionID, sup)
			return nil, ctx.Err()
		}
		dec := sup.Record(idx, verdict, needs)
		var amendmentStop *planAmendmentLimit
		if errors.As(stepErr, &amendmentStop) {
			st.Status, st.Reason = "blocked", amendmentStop.Error()
			dec.HandBack, dec.Action = true, "blocked"
		}
		var boundedRecoveryStop *recoveryCycleStop
		if errors.As(stepErr, &boundedRecoveryStop) && !verdict.Pass {
			// The checkpoint policy already consumed its bounded repair cycles.
			// A fresh automatic attempt would erase that limit and repeat the
			// investigation. Retain evidence and await explicit user recovery.
			st.Status = "blocked"
			dec.HandBack = true
			dec.Action = "blocked"
		}
		if dec.Action == "retry" {
			first = idx
		}
		if verdict.Pass && needs < 0 && reviewRequested == nil {
			for _, prior := range stale {
				if selected && !scope.includes(prior) {
					dec.HandBack = true
					st.Reason = "Shared-file evidence is stale outside the selected scope; run the whole plan to recheck it."
					continue
				}
				if err := h.boundary(ctx); err != nil {
					return nil, err
				}
				rv := l.verifyStep(attemptCtx, wr, sessionID, prior, plan.Steps[prior].Verify)
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				sup.Steps[prior].Verdict = &rv
				if rv.Pass {
					sup.Steps[prior].Status = "passed"
				} else {
					sup.Steps[prior].Status = "blocked"
					sup.Steps[prior].Reason = "Recovery shared-file check failed: " + rv.Evidence
					dec.HandBack = true
				}
				if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
					return nil, err
				}
			}
		}
		if selected && needs >= 0 && !scope.includes(needs) {
			// Record has invalidated the target and downstream evidence. Leave
			// that truth persisted, but require a new explicit scope to fix it.
			dec.HandBack = true
			st.Reason = fmt.Sprintf("Step %d requires revision of unselected step %d. Select the prerequisite or run the whole plan. %s", idx+1, needs+1, summary)
			verdict.Evidence = st.Reason
			st.Verdict.Evidence = st.Reason
		}

		if needs >= 0 && dec.Action == "revise" {
			sup.Steps[needs].Reason = summary
		}
		if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
			return nil, err
		}
		_, planID, _ := LatestPlan(l.path(sessionID))
		cpStatus := statusFor(sup.Steps[idx].Status)
		if needs >= 0 {
			cpStatus = "pending"
		}
		if _, err := wr.Append(episodic.Checkpoint, map[string]any{"plan_event_id": planID, "index": idx, "step": st.Title, "status": cpStatus, "rev": st.Rev, "check": verdict.Check, "evidence": verdict.Evidence, "decision": dec.Action, "projection_only": true}); err != nil {
			return nil, err
		}
		// Reverify is emitted only after the corrected target has passed.
		for _, d := range dec.Reverify {
			if selected && !scope.includes(d) {
				continue // remains needs_reverify until a subsequent authorized run
			}
			if err := h.boundary(ctx); err != nil {
				return nil, err
			}
			rv := l.verifyStep(ctx, wr, sessionID, d, plan.Steps[d].Verify)
			sup.Steps[d].Verdict = &rv
			if rv.Pass {
				sup.Steps[d].Status = "passed"
			} else {
				sup.ReverifyFailed(d, rv)
			}
			remaining := sup.reverify[:0]
			for _, x := range sup.reverify {
				if x != d {
					remaining = append(remaining, x)
				}
			}
			sup.reverify = remaining
			if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
				return nil, err
			}
		}
		if len(sup.reverify) == 0 {
			sup.revisionTarget = -1
		}
		if dec.HandBack {
			handback = true
			break
		}
		if recoveryBrief != "" && verdict.Pass && !single {
			if err := h.recoveryPhase("continuing"); err != nil {
				return nil, err
			}
		}
		if single {
			break
		}
	}
	if attempts >= 3*len(plan.Steps)+4 {
		handback = true
	}
	if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
		return nil, err
	}
	results := make([]StepResult, len(plan.Steps))
	for i, st := range sup.Steps {
		results[i] = StepResult{Step: st.Title, Status: statusFor(st.Status), Summary: stepSummary(st)}
	}
	report := renderReport(plan, results, handback)
	if selected {
		if scope.done(sup) {
			report = "Selected steps passed. Unselected steps were not executed.\n\n" + report
		} else {
			report = "Selected execution stopped before every selected step passed.\n\n" + report
		}
	}
	if _, err := wr.Append(episodic.MsgAssistant, map[string]any{"text": report}); err != nil {
		return nil, err
	}
	if _, err := wr.Append(episodic.TurnClose, map[string]any{"autopilot": true, "handback": handback, "plan_complete": sup.Done(), "selected_steps": scope}); err != nil {
		return nil, err
	}
	l.runBoundary(sessionID)
	stop := StopFinalAnswer
	if handback {
		stop = "plan_blocked"
	}
	h.mu.Lock()
	calls := h.state.Calls
	h.mu.Unlock()
	return &Result{Reply: report, Iterations: calls, StopReason: stop, Pulls: len(pulls)}, nil
}

type revisionRequest struct {
	target int
	reason string
}

func (r *revisionRequest) Error() string { return r.reason }

func (l *Loop) runStep(ctx context.Context, wr *episodic.Writer, sid, systemPrompt string, mode Mode, p *Plan, idx int, pulls []memory.Pull, sp StepPrompt) (string, error) {
	// Every step/retry builds a fresh model window. Its first unqualified
	// read must start at the beginning, without resetting other run owners.
	ctx = tools.WithFreshReadCursor(ctx)
	h := handleOf(ctx)
	reg := h.registry
	events, err := episodic.Replay(l.path(sid))
	if err != nil {
		return "", err
	}
	planID := ""
	for n := len(events) - 1; n >= 0; n-- {
		if events[n].Type == episodic.Plan {
			planID = events[n].ID
			break
		}
	}
	snapshot, snapshotID, err := ReducePlan(events)
	if err != nil {
		return "", err
	}
	if snapshotID != planID {
		return "", &planSnapshotError{"plan snapshot changed before step execution"}
	}
	if !samePlanSnapshot(p, snapshot.Plan) {
		return "", &planSnapshotError{"step plan does not match the current committed snapshot"}
	}
	reader, err := newPlanStepReader(planID, p, snapshot.Steps)
	if err != nil {
		return "", err
	}
	reg, err = reg.WithScopedEntry(tools.Entry{Tool: reader, RiskTier: tools.RiskSafe, Modes: []string{tools.ModeAutopilot}})
	if err != nil {
		return "", err
	}
	reviewTool := &verificationReviewDispatcher{path: l.path(sid), sessionID: sid, runID: h.state.ID, planID: planID, index: idx}
	if p.Steps[idx].Verify != nil {
		reviewTool.original = *p.Steps[idx].Verify
		reg, err = reg.WithScopedEntry(tools.Entry{Tool: reviewTool, RiskTier: tools.RiskSafe, Modes: []string{tools.ModeAutopilot}})
		if err != nil {
			return "", err
		}
	}
	baseRegistry := h.registry
	adaptationTool := &planAdaptationDispatcher{writer: wr, sessionID: sid, runID: h.state.ID, planID: planID, index: idx, workspace: h.state.Workspace, current: p}
	reg, err = reg.WithScopedEntry(tools.Entry{Tool: adaptationTool, RiskTier: tools.RiskSafe, Modes: []string{tools.ModeAutopilot}})
	if err != nil {
		return "", err
	}
	h.registry = reg
	defer func() { h.registry = baseRegistry }()
	progress := restoreRecoveryProgress(events, h.state.Workspace, planID, idx, p.Steps[idx])
	h.mu.Lock()
	recovering := h.state.RecoveryPhase != ""
	h.mu.Unlock()
	goal := effectiveStepPrompt(sp, p, idx, planID, h.state.Workspace)
	items := []window.Item{
		{Msg: llm.Message{Role: "system", Content: systemPrompt}, Kind: "system"},
		{Msg: llm.Message{Role: "user", Content: "Task constraints (do only the active step):\n" + h.brief + "\nPlan: " + p.Title + "\n" + goal}, Kind: "pinned"},
	}
	if recovering {
		if memory := progress.brief(); memory != "" {
			items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: memory}, Kind: "pinned"})
		}
	}
	planImages, imageErr := taskImages(l.path(sid))
	if imageErr != nil {
		return "", imageErr
	}
	if len(planImages) > 0 {
		items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: "Images supplied with the planning request:", Images: planImages}, Kind: "pinned"})
	}
	if text := wrapReminder(memory.FormatPulls(pulls)); text != "" {
		items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "pulls"})
	}
	g := newTurnGuardBudget(0, maxStepTime)
	work := newWorkTracker(h.state.Workspace)
	extFP, _ := work.fingerprint()
	initialFP := extFP
	shapeRetries, inspectionRounds := 0, 0
	coached := false
	inspectionExtended := false
	var cycle *recoveryCycle
	checkItem := -1
	focus := newRecoveryFocus()
	focusItem := -1
	if recovering && p.Steps[idx].Verify.Validate() == nil {
		version := recoveryStepVersion(h.state.Workspace, p.Steps[idx])
		cycle = newRecoveryCycle(version)
		baseline := l.verifyStep(ctx, wr, sid, idx, p.Steps[idx].Verify)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		cycle.checked(version, baseline, true)
		if baseline.Pass && sp.RecoveryCanSkip {
			return "The current committed check already passes; no repair was needed. Continue only after required shared-file rechecks.", nil
		}
		checkItem = len(items)
		items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: recoveryCheckPrompt(baseline, p.Steps[idx].Verify, true)}, Kind: "pinned"})
		if !baseline.Pass {
			text, err := l.focusRecovery(ctx, wr, sid, reg, progress, focus, p.Steps[idx], baseline)
			if err != nil {
				return "", err
			}
			focusItem = len(items)
			items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "pinned"})
		}
		if err := h.recoveryPhase("diagnosing"); err != nil {
			return "", err
		}
	}
	level := h.thinkingFor(mode.Name, false)
	seenSteer := map[string]bool{}
	if events, err := episodic.Replay(l.path(sid)); err == nil {
		for _, ev := range events {
			seenSteer[ev.ID] = true
		}
	}
	for i := 1; ; i++ {
		wasPaused := h.paused.Load()
		if err := h.boundary(ctx); err != nil {
			return "", err
		}
		if wasPaused {
			g.progress()
		}
		if events, err := episodic.Replay(l.path(sid)); err == nil {
			for _, ev := range events {
				if seenSteer[ev.ID] || ev.Type != episodic.MsgUser {
					continue
				}
				seenSteer[ev.ID] = true
				var msg UserMessage
				if json.Unmarshal(ev.Payload, &msg) == nil {
					images, imageErr := llm.ValidateImages(msg.Images)
					if imageErr != nil {
						return "", imageErr
					}
					items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: msg.Text, Images: images}, Kind: "pinned", EvtID: ev.ID})
				}
			}
		}
		fp, _ := work.fingerprint()
		g.observeWorkspace(fp)
		boundary := i > g.maxIter+g.iterExts*g.maxIter
		if cycle != nil {
			version := recoveryStepVersion(h.state.Workspace, p.Steps[idx])
			if cycle.due(version, i, boundary) {
				verdict := l.verifyStep(ctx, wr, sid, idx, p.Steps[idx].Verify)
				if err := ctx.Err(); err != nil {
					return "", err
				}
				cycle.checked(version, verdict, false)
				// Keep exactly one pinned check, placed AFTER the repair group.
				// Replacing the old prompt in place left later obsolete tool
				// failures looking newer than the authoritative checkpoint.
				items[checkItem].Kind = "dropped"
				checkItem = len(items)
				items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: recoveryCheckPrompt(verdict, p.Steps[idx].Verify, false)}, Kind: "pinned"})
				text := fmt.Sprintf("Repair check %d/3 still fails: %s. Investigate this result before the next repair; changed output is not proof of improvement.", cycle.failedCycles, clipEvidence(verdict.Evidence))
				if verdict.Pass {
					text = "Repair passed the committed check. Required shared-file rechecks, including previously passed later steps, still decide whether the plan continues."
				}
				if _, err := wr.Append(episodic.Note, map[string]any{"kind": "recovery_check_checkpoint", "index": idx, "failed_cycles": cycle.failedCycles, "verdict": verdict, "text": text}); err != nil {
					return "", err
				}
				if verdict.Pass {
					return "Repair passed the committed check; required shared-file rechecks still decide continuation.", nil
				}
				if cycle.exhausted() {
					return "", &recoveryCycleStop{progress.stopDetail("3 repair/check cycles did not pass the committed check; automatic retry paused. Resume retains the evidence and must investigate the latest check")}
				}
				focusText, err := l.focusRecovery(ctx, wr, sid, reg, progress, focus, p.Steps[idx], verdict)
				if err != nil {
					return "", err
				}
				if focusItem >= 0 {
					items[focusItem].Kind = "dropped"
				}
				focusItem = len(items)
				items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: focusText}, Kind: "pinned"})
				if err := h.recoveryPhase("diagnosing"); err != nil {
					return "", err
				}
			}
		}
		if boundary && cycle != nil && cycle.takeCredit() && g.extendIter() {
			extFP = fp
			text := "A changed source version produced a new committed-check result, not a pass. Granting one bounded diagnostic slice; failed repair/check cycles are not reset."
			items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "pinned"})
			if _, err := wr.Append(episodic.Note, map[string]string{"kind": "recovery_checked_continuation", "text": text}); err != nil {
				return "", err
			}
		}
		if boundary && !recovering && fp != extFP && g.extendIter() {
			extFP = fp
			wr.Append(episodic.Note, map[string]string{"kind": "iteration_checkpoint", "text": "Workspace changed; granting one bounded additional attempt budget."})
		}
		if recovering && !inspectionExtended && (cycle == nil || cycle.failedCycles == 0) && i > g.maxIter+g.iterExts*g.maxIter {
			// Actual newly inspected source can require more than eight calls
			// before the first safe repair. Keep that same window ONCE, using
			// one existing extension slot; repeated or stale ranges earn zero.
			// This is inspection continuity, not a claim of repair progress.
			if bytes := progress.coverage.freshBytes(); bytes >= 1024 && g.extendIter() {
				inspectionExtended = true
				extFP = fp
				text := fmt.Sprintf("RECOVERY SOURCE CONTINUATION: %d new, current source bytes inspected. Keeping this window for one bounded %d-call slice using an existing extension slot. Do not restart diagnosis; use the acquired source for a targeted repair and verification, or report the precise blocker. Further extensions require changed declared source plus a newly observed committed-check result; all other guards remain active.", bytes, g.maxIter)
				items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "pinned"})
				if _, err := wr.Append(episodic.Note, map[string]any{"kind": "recovery_source_continuation", "new_source_bytes": bytes, "text": text}); err != nil {
					return "", err
				}
			}
		}
		if g.tokensExhausted() && g.extendTokens() {
			wr.Append(episodic.Note, map[string]string{"kind": "token_checkpoint", "text": "Continuing with an additional bounded token slice."})
		}
		if _, detail, tripped := g.preThink(i); tripped {
			if recovering {
				detail = progress.stopDetail(detail)
				return "", &recoveryCycleStop{detail}
			}
			return "", fmt.Errorf("guard: %s", detail)
		}
		if recovering && !coached && inspectionRounds >= 4 && fp == initialFP {
			if len(progress.entries) > 0 && progress.newFacts == 0 {
				return "", &recoveryCycleStop{progress.stopDetail("four recovery rounds repeated retained evidence without new information or a workspace change")}
			}
			text := "RECOVERY CHECKPOINT: four model rounds without a workspace change. Acquired evidence is retained across retries. Use the evidence already read. Make one small, targeted structured repair if supported, then check it; otherwise report the exact missing evidence or blocker. Use read's actual returned coverage and next cursor, not the requested range. For edit, use a unique raw source fragment with expected_sha256 from a current read; line-number prefixes are not source. Do not keep rereading the same source or attempt a whole-module rewrite. An identical-current snapshot cannot recover missing code; use recovery_read query to find older recorded source if needed. Do not weaken the committed check."
			items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "pinned"})
			if _, err := wr.Append(episodic.Note, map[string]string{"kind": "recovery_checkpoint", "text": text}); err != nil {
				return "", err
			}
			coached = true
		}
		// Refresh the effective frame without restarting this model window or
		// its budgets. Include the harness note receipt (not just its child
		// tool-result ID) so command checks can be cited without journal spelunking.
		goal = effectiveStepPrompt(sp, p, idx, planID, h.state.Workspace) + adaptationTool.evidencePrompt()
		items[1].Msg.Content = "Task constraints (do only the active step):\n" + h.brief + "\nPlan: " + p.Title + "\n" + goal
		msgs, _ := l.compress(ctx, items)
		specs := reg.Specs(mode.Name)
		filtered := specs[:0:0]
		for _, s := range specs {
			if s.Function.Name != "commit_plan" {
				filtered = append(filtered, s)
			}
		}
		specs = filtered
		if idx > 0 {
			specs = append(specs, llm.ToolSpec{Type: "function", Function: llm.FunctionSpec{Name: "request_revision", Description: "Stop this step and request a correction to an earlier step. Zero-based target; explain exactly what must change.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"target": map[string]any{"type": "integer", "minimum": 0, "maximum": idx - 1}, "reason": map[string]any{"type": "string"}}, "required": []string{"target", "reason"}}}})
		}
		reply, usage, err := l.completeWithRetry(llm.WithThinking(ctx, level), wr, msgs, specs, "", mode.ProseCap)
		g.addUsage(usage) // retain completed decode effort even if a steer/pause discards the reply
		if errors.Is(err, errControl) {
			h.steered.Store(false)
			i--
			g.progress()
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage)); err != nil {
			return "", err
		}
		if reply.Truncated() {
			// Empty length-stopped output contains no executable action. Reissue
			// once without spending the remaining action iterations. Repeating
			// another long reasoning-only decode cannot repair source.
			// Token/time guards and the overall model-call telemetry still count.
			if shapeRetries >= 1 || level == llm.ThinkingOff {
				if recovering {
					return "", &recoveryCycleStop{progress.stopDetail("model output limit reached without an answer or tool call; bounded output recovery exhausted")}
				}
				return "", fmt.Errorf("model output limit reached without an answer or tool call; bounded output recovery exhausted")
			}
			shapeRetries++
			level = llm.ThinkingOff
			text := fmt.Sprintf("Output limit reached before an answer or tool call; no action was executed. Bounded output retry %d/1 uses %s thinking. Reasoning and output both count toward the total effort budget. Make one small tool call or complete targeted helper/hunk, not a whole-module reconstruction; if the evidence is insufficient, report a precise blocker.", shapeRetries, level)
			if _, err := wr.Append(episodic.Note, map[string]string{"kind": "output_retry", "text": text}); err != nil {
				return "", err
			}
			items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "pinned"})
			i--
			continue
		}
		inspectionRounds++
		if len(reply.ToolCalls) == 0 {
			if strings.TrimSpace(reply.Content) == "" {
				return "", fmt.Errorf("model returned no answer or tool call")
			}
			return reply.Content, nil
		}
		items = append(items, window.Item{Msg: llm.Message{Role: "assistant", Content: reply.Content, ToolCalls: reply.ToolCalls}, Kind: "assistant"})
		for _, tc := range reply.ToolCalls {
			if err := h.boundary(ctx); err != nil {
				return "", err
			}
			if tc.Function.Name == "request_revision" {
				var r struct {
					Target *int
					Reason string
				}
				if json.Unmarshal([]byte(tc.Function.Arguments), &r) == nil && r.Target != nil && *r.Target >= 0 && *r.Target < idx && strings.TrimSpace(r.Reason) != "" {
					wr.Append(episodic.ToolCall, map[string]any{"id": tc.ID, "name": tc.Function.Name, "args": json.RawMessage(tc.Function.Arguments)})
					wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": true, "output": "revision requested: " + r.Reason})
					return "", &revisionRequest{*r.Target, r.Reason}
				}
			}
			beforeVersion := ""
			if cycle != nil {
				beforeVersion = recoveryStepVersion(h.state.Workspace, p.Steps[idx])
			}
			out, execErr, eventID := l.executeCall(ctx, wr, reg, specs, mode.Name, tc)
			if adaptationTool.stopped != nil {
				return "", adaptationTool.stopped
			}
			if reviewTool.requested != nil {
				if execErr != nil {
					return "", execErr
				}
				return "", reviewTool.requested
			}
			// Retain observations even on the initial attempt: a later retry must
			// not lose source acquired before the first failure. The journal is
			// authoritative; this note only indexes bounded, versioned evidence.
			if err := progress.observe(wr, tc, eventID, out, execErr); err != nil {
				return "", err
			}
			visible := out
			if eventID != "" {
				visible = "Evidence receipt: " + eventID + " (journal reference, not source text)\n" + out
			}
			items = append(items, window.Item{Msg: llm.Message{Role: "tool", ToolCallID: tc.ID, Content: visible}, Kind: "tool", EvtID: eventID})
			if adaptationTool.applied != nil {
				// Refresh exactly the pinned frame and read-only plan snapshot.
				// g, progress, cycle, model/time budgets and tool history stay live.
				fresh := adaptationTool.applied
				*p = *fresh.Plan
				updated, err := newPlanStepReader(planID, p, fresh.Steps)
				if err != nil {
					return "", err
				}
				reader.steps = updated.steps
				adaptationTool.applied = nil
			}
			fp, _ = work.fingerprint()
			g.observeWorkspace(fp)
			if cycle != nil {
				// Recovery bash is read-only, but observe content for every tool:
				// alternate tool names may not bypass the repair/check discipline.
				version := recoveryStepVersion(h.state.Workspace, p.Steps[idx])
				if version != beforeVersion {
					cycle.repair(version, i)
				}
			}
			if execErr != nil {
				if detail, tripped := g.toolError(tc.Function.Name, out); tripped {
					if recovering {
						return "", &recoveryCycleStop{progress.stopDetail(detail)}
					}
					return "", fmt.Errorf("%s: %s", detail, out)
				}
			} else {
				g.toolOK(tc.Function.Name)
				if tc.Function.Name != planAdaptationName && !g.seenBefore(tc.Function.Name, json.RawMessage(tc.Function.Arguments), out) {
					g.progress()
				}
			}
			if detail, tripped := g.repeatedResult(tc.Function.Name, json.RawMessage(tc.Function.Arguments), out); tripped {
				if recovering {
					return "", &recoveryCycleStop{progress.stopDetail(detail)}
				}
				return "", fmt.Errorf("guard: %s", detail)
			}
		}
	}
}

func renderReport(plan *Plan, results []StepResult, handback bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Autopilot report — %s\n\n", plan.Title)
	counts := map[string]int{}
	for i, r := range results {
		icon := "✓"
		switch r.Status {
		case "failed", "blocked":
			icon = "✗"
			counts["blocked"]++
		case "skipped", "pending", "needs_reverify", "unverified":
			icon = "·"
			counts[r.Status]++
		case "done":
			counts["done"]++
		default:
			icon = "·"
			counts[r.Status]++
		}
		fmt.Fprintf(&sb, "%s %d. %s — %s\n   %s\n", icon, i+1, r.Step, planStatusLabel(r.Status), strings.TrimSpace(r.Summary))
	}
	var totals []string
	for _, status := range []string{"done", "needs_reverify", "blocked", "running", "verifying", "pending", "unverified", "skipped"} {
		if counts[status] > 0 {
			totals = append(totals, fmt.Sprintf("%d %s", counts[status], planStatusLabel(status)))
		}
	}
	sb.WriteString("\n" + strings.Join(totals, " · "))
	if handback {
		sb.WriteString("\nWork is paused for a decision. Inspect the failed check or budget stop before retrying or revising the plan.")
	}
	return sb.String()
}

// outOfPlanNote flags a write that creates a file the committed plan never
// declared. Architecture drift (the model inventing extra files) was invisible
// before — a one-file build silently became four files and only failed at
// runtime. The note doesn't block the write (plans adapt); it makes the drift
// something the model and the user can SEE and correct.
func outOfPlanNote(p *Plan, tool string, args []byte) string {
	if p == nil || tool != "write" {
		return ""
	}
	declared := map[string]bool{}
	for _, s := range p.Steps {
		for _, f := range s.Files {
			declared[f] = true
		}
	}
	if len(declared) == 0 {
		return "" // a plan without file declarations constrains nothing
	}
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &a) != nil || a.Path == "" || declared[a.Path] {
		return ""
	}
	files := make([]string, 0, len(declared))
	for f := range declared {
		files = append(files, f)
	}
	sort.Strings(files)
	return fmt.Sprintf("note: %s is not in the committed plan (declared files: %s). "+
		"If this extra file is intentional, continue; otherwise keep to the planned layout.",
		a.Path, strings.Join(files, ", "))
}

// autoCommitPlanFile is the harness-side answer to the model's stubbornest
// habit: writing its plan to a .md file instead of calling commit_plan. If a
// write is plan-shaped (plan-named markdown with parseable steps) and it
// parses, the harness commits the structured plan itself — the file still
// lands on disk, AND the plan card + Planner see a real plan event. The
// translation is disclosed in the returned note. Prompt pleading demonstrably
// failed here twice; translation always runs.
func autoCommitPlanFile(wr *episodic.Writer, tool string, args []byte) (*Plan, string) {
	if tool != "write" {
		return nil, ""
	}
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if json.Unmarshal(args, &a) != nil || !tools.PlanLike(a.Path, a.Content) {
		return nil, ""
	}
	title, parsed := tools.ParsePlanMarkdown(a.Content)
	if title == "" {
		title = a.Path
	}
	steps := make([]PlanStep, 0, len(parsed))
	for _, p := range parsed {
		steps = append(steps, PlanStep{Title: p.Title, Detail: p.Detail, Files: p.Files, Risk: p.Risk})
	}
	plan := &Plan{Title: title, Steps: steps, AutonomyBudget: "low"}
	if _, err := wr.Append(episodic.Plan, map[string]any{
		"title": plan.Title, "steps": plan.Steps, "autonomy_budget": plan.AutonomyBudget,
	}); err != nil {
		return nil, ""
	}
	return plan, fmt.Sprintf("note: %s looked like a plan, so it was ALSO committed as a structured plan (%d steps) — it now appears in the plan card. Next time call commit_plan directly.", a.Path, len(steps))
}

// planFromText translates a plan the model wrote as XML-style TEXT into a
// committed plan. With thinking on, Qwen3.8 twice answered the planning
// call with
//
//	<commit_plan><steps><step name="skeleton" files="a.html" description="…">…</step>…
//
// — a tool call in its head, in a shape the tool parser does not know. Same
// rule as plan-shaped .md writes: translate, don't plead. Returns nil when
// the text has no such steps.
func planFromText(wr *episodic.Writer, text string) (*Plan, string) {
	if !strings.Contains(text, "<step") {
		return nil, ""
	}
	var steps []PlanStep
	for _, m := range xmlSteps(text) {
		attrs, body := m[0], strings.TrimSpace(m[1])
		st := PlanStep{Title: xmlAttr(attrs, "name", "title"), Detail: xmlAttr(attrs, "description", "detail")}
		if st.Detail == "" {
			st.Detail = body
		}
		if st.Title == "" {
			st.Title = firstLine(body)
		}
		for _, f := range strings.FieldsFunc(xmlAttr(attrs, "files", "file"), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
			st.Files = append(st.Files, strings.TrimSpace(f))
		}
		if st.Title != "" {
			steps = append(steps, st)
		}
	}
	if len(steps) == 0 {
		return nil, ""
	}
	title := xmlAttr(text, "title", "name")
	if title == "" || len(title) > 80 {
		title = "Plan"
	}
	plan := &Plan{Title: title, Steps: steps, AutonomyBudget: "low"}
	if _, err := wr.Append(episodic.Plan, map[string]any{
		"title": plan.Title, "steps": plan.Steps, "autonomy_budget": plan.AutonomyBudget,
	}); err != nil {
		return nil, ""
	}
	return plan, fmt.Sprintf("your plan was written as text, not as a commit_plan tool call — it was translated and committed anyway (%d steps). Next time call the tool. Start on step 1 now.", len(steps))
}

var (
	// an opening <step …> tag; attribute values may hold '>' inside quotes
	xmlStepOpen = regexp.MustCompile(`(?is)<step\b((?:"[^"]*"|[^>"])*?)(/?)>`)
	xmlStepEnd  = regexp.MustCompile(`(?i)</step>`)
	xmlAttrRe   = regexp.MustCompile(`(?i)\b([a-z_]+)\s*=\s*"([^"]*)"`)
)

// xmlSteps returns (attrs, body) for every step in document order, whether
// written as <step …>body</step> (Crane5) or self-closing <step … /> with a
// description attribute (Crane6). A step without a closing tag ends at the
// next <step or at the end of the text.
func xmlSteps(text string) [][2]string {
	var out [][2]string
	locs := xmlStepOpen.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		attrs := text[loc[2]:loc[3]]
		selfClosing := loc[5] > loc[4] // the "/" group matched
		body := ""
		if !selfClosing {
			end := len(text)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			seg := text[loc[1]:end]
			if m := xmlStepEnd.FindStringIndex(seg); m != nil {
				seg = seg[:m[0]]
			}
			body = seg
		}
		out = append(out, [2]string{attrs, body})
	}
	return out
}

// xmlAttr returns the first of the named attributes present in attrs.
func xmlAttr(attrs string, names ...string) string {
	found := map[string]string{}
	for _, m := range xmlAttrRe.FindAllStringSubmatch(attrs, -1) {
		if _, ok := found[strings.ToLower(m[1])]; !ok {
			found[strings.ToLower(m[1])] = strings.TrimSpace(m[2])
		}
	}
	for _, n := range names {
		if v := found[n]; v != "" {
			return v
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// stepRunContext is what a step's run is told about the ground it stands on:
// the steps already verified, and — for a revision — who asked for it and why.
func stepRunContext(sup *Supervisor, idx int, steer string) string {
	parts := []string{}
	if strings.TrimSpace(steer) != "" {
		parts = append(parts, "The user says: "+strings.TrimSpace(steer))
	}
	if c := StepContext(sup); c != "" {
		parts = append(parts, c)
	}
	// The last attempt's verdict, whether this is a retry or a revision. A
	// retry used to start blind: the harness ran the check at the cap, saw
	// "Uncaught TypeError: Cannot read properties of undefined (reading 'x')"
	// 2,128 times in the console, recorded it in the checkpoint — and the
	// next attempt's fresh window never heard of it. The model re-read its
	// file, edited blind, and ran out again (NFQ step 4, 2026-09-05).
	if st := sup.Steps[idx]; st.Verdict != nil && !st.Verdict.Pass {
		parts = append(parts, "Your previous attempt at this step FAILED its check: "+st.Verdict.Check+
			"\nWhat was observed: "+clipEvidence(st.Verdict.Evidence)+
			"\nInvestigate this result first. Use read_plan_step for exact saved contracts. Repair the implementation and rerun the unchanged check; if evidence shows the generated criterion conflicts with the task or fixture, use request_verification_review rather than distorting the fixture or metrics.")
	}
	if st := sup.Steps[idx]; st.Verdict != nil && st.Verdict.VerificationReview != nil {
		parts = append(parts, verificationReviewSummary(*st.Verdict)+"\nRetry is not approval of the proposal. The original committed check remains authoritative.")
	}
	return strings.Join(parts, "\n\n")
}

// reNeedsStep finds a run asking for an EARLIER step to be reopened.
//
// The model asks; the harness never guesses. A run that discovers step 1 should
// have declared a variable it needs says so in its report, and the supervisor
// turns that into a revision run rather than letting the model patch around it
// here — which is how a later step quietly grows a workaround for an earlier
// step's omission.
var reNeedsStep = regexp.MustCompile(`(?i)\bstep\s+(\d+)\s+(?:must|needs to|should|has to)\b`)

// needsStepFrom reports the 0-based index of an earlier step the run asked to
// reopen, or -1. Only EARLIER steps count: a run naming a later step is
// describing what comes next, not a dependency it is blocked on.
func needsStepFrom(summary string, idx int) int {
	m := reNeedsStep.FindStringSubmatch(summary)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return -1
	}
	if target := n - 1; target < idx {
		return target
	}
	return -1
}

// statusFor maps the supervisor's step state to the report's vocabulary.
func statusFor(s string) string {
	switch s {
	case "passed":
		return "done"
	case "blocked":
		return "failed"
	case "failed":
		return "failed"
	}
	return s
}

// summaryFor prefers what was OBSERVED over what was claimed. The model's own
// sentence is kept as context, never as the verdict.
func summaryFor(v Verdict, modelSummary string) string {
	if v.VerificationReview != nil {
		return verificationReviewSummary(v)
	}
	if v.Pass {
		if v.Check == "no check declared" {
			return modelSummary
		}
		return "verified: " + v.Check
	}
	if v.Evidence != "" {
		text := "check failed (" + v.Check + "): " + clipEvidence(v.Evidence)
		if v.EvidenceEventID != "" {
			text += "\nFull check evidence: " + v.EvidenceEventID
		}
		if v.ExecutionStop != "" {
			text += "\nExecution stop (separate from the check): " + clipEvidence(v.ExecutionStop)
		}
		return text
	}
	return "check failed: " + v.Check
}

func clipEvidence(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		// Prefer an actual diagnostic over a long echoed node -e command.
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Error:") || strings.HasPrefix(line, "AssertionError") || strings.HasPrefix(line, "SyntaxError:") || strings.HasPrefix(line, "TypeError:") || strings.HasPrefix(line, "ReferenceError:") {
				return recoveryExcerpt(line, 300)
			}
		}
		return recoveryExcerpt(s, 300)
	}
	return s
}

// planningSourcesCap bounds how much read material rides into each step.
const planningSourcesCap = 40000

// planningSources collects what the model read while planning THIS turn: the
// tool results of read / grep / outline_file between the last user message
// and the plan event. Every step then starts knowing the source the plan
// describes, instead of re-reading it under its own budget.
func planningSources(eventsPath string) string {
	events, err := episodic.Replay(eventsPath)
	if err != nil {
		return ""
	}
	start := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.MsgUser {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	var b strings.Builder
	calls := map[string]string{} // tool call id → "name path"
	for _, ev := range events[start:] {
		switch ev.Type {
		case episodic.Plan:
			// reads AFTER the plan are a step's own, not planning
			return finishSources(&b)
		case episodic.ToolCall:
			var c struct {
				ID   string          `json:"id"`
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			}
			if json.Unmarshal(ev.Payload, &c) == nil {
				var a struct {
					Path string `json:"path"`
				}
				json.Unmarshal(c.Args, &a)
				calls[c.ID] = c.Name + " " + a.Path
			}
		case episodic.ToolResult:
			var r struct {
				ID     string `json:"id"`
				OK     bool   `json:"ok"`
				Output string `json:"output"`
			}
			if json.Unmarshal(ev.Payload, &r) != nil || !r.OK {
				continue
			}
			what := calls[r.ID]
			if !(strings.HasPrefix(what, "read ") || strings.HasPrefix(what, "grep ") || strings.HasPrefix(what, "outline_file ")) {
				continue
			}
			if b.Len()+len(r.Output) > planningSourcesCap {
				continue
			}
			if b.Len() == 0 {
				b.WriteString("SOURCE you already read while planning — do not re-read it, build from it:\n")
			}
			fmt.Fprintf(&b, "\n--- %s ---\n%s\n", what, r.Output)
		}
	}
	return finishSources(&b)
}

func finishSources(b *strings.Builder) string {
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}
