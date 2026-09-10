package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
)

const planAdaptationName = "amend_implementation_guidance"
const maxPlanAmendments = 6
const maxStepAmendments = 2

type planAmendmentInput struct {
	ExpectedRevision         string   `json:"expected_revision"`
	StepID                   string   `json:"step_id"`
	ExpectedWorkspaceVersion string   `json:"expected_workspace_version"`
	Guidance                 string   `json:"guidance"`
	Reason                   string   `json:"reason"`
	EvidenceEventIDs         []string `json:"evidence_event_ids"`
}

// PlanAmendment is an append-only model-guidance ledger, not a new acceptance
// contract. No API field can replace a step, check, fixture, scope or budget.
// The full original step and plan contract hash remain bound to each entry.
type PlanAmendment struct {
	ID               string                       `json:"id"`
	EventID          string                       `json:"event_id,omitempty"`
	PlanID           string                       `json:"plan_event_id"`
	SessionID        string                       `json:"session_id"`
	RunID            string                       `json:"run_id"`
	Index            int                          `json:"index"`
	Authority        string                       `json:"authority"`
	Input            planAmendmentInput           `json:"input"`
	PreviousGuidance string                       `json:"previous_guidance"`
	OriginalStep     PlanStep                     `json:"original_step"`
	ContractSHA256   string                       `json:"contract_sha256"`
	Evidence         []VerificationReviewEvidence `json:"evidence"`
	Impact           string                       `json:"impact"`
}

func immutablePlanSHA(p *Plan) string {
	copy := *p
	copy.Revision, copy.Guidance, copy.Amendments = "", nil, nil
	raw, _ := json.Marshal(copy)
	return recoverySHA(raw)
}

type planAmendmentLimit struct{ reason string }

func (e *planAmendmentLimit) Error() string {
	return e.reason + "; explicit user review is required (contract and spent budgets unchanged)"
}

func normalizedGuidance(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// Validation requires a current failed harness observation for this exact
// workspace/check, plus real scoped evidence. Evidence establishes provenance,
// not semantic correctness or authority to change an acceptance requirement.
func newPlanAmendment(events []episodic.Event, p *Plan, sessionID, runID, planID string, index int, version string, args string) (*PlanAmendment, error) {
	if p == nil || sessionID == "" || runID == "" || planID == "" || index < 0 || index >= len(p.Steps) || version == "" {
		return nil, fmt.Errorf("guidance amendment requires an active plan, step and workspace")
	}
	if len(args) > 16000 {
		return nil, fmt.Errorf("guidance amendment arguments exceed 16000 bytes")
	}
	var in planAmendmentInput
	if err := decodeVerificationReviewJSON(args, &in); err != nil {
		return nil, fmt.Errorf("invalid guidance amendment (requirements/check changes require review): %w", err)
	}
	in.Guidance, in.Reason = strings.TrimSpace(in.Guidance), strings.TrimSpace(in.Reason)
	if in.ExpectedRevision != effectivePlanRevision(planID, p) || in.StepID != plan.StepID(p.Steps[index].ID, index) {
		return nil, fmt.Errorf("stale plan revision or step ID; read_plan_step before amending")
	}
	if in.ExpectedWorkspaceVersion != version {
		return nil, fmt.Errorf("workspace changed; expected_workspace_version is now %s; obtain a fresh unchanged-check result before amending", version)
	}
	if in.Guidance == "" || len(in.Guidance) > 4000 || in.Reason == "" || len(in.Reason) > 4000 || len(in.EvidenceEventIDs) < 1 || len(in.EvidenceEventIDs) > 8 {
		return nil, fmt.Errorf("guidance and reason require 1..4000 bytes each and 1..8 evidence_event_ids")
	}
	count := 0
	for _, previous := range p.Amendments {
		if previous.Input.StepID != in.StepID {
			continue
		}
		count++
		if normalizedGuidance(previous.Input.Guidance) == normalizedGuidance(in.Guidance) {
			return nil, &planAmendmentLimit{"Equivalent implementation guidance was already tried"}
		}
	}
	if count >= maxStepAmendments || len(p.Amendments) >= maxPlanAmendments {
		return nil, &planAmendmentLimit{"Bounded implementation-guidance amendment ledger is exhausted"}
	}
	start := -1
	var owner reviewReplayOwner
	for i, ev := range events {
		if ev.Type == episodic.Plan {
			start = i
			if ev.ID != planID {
				start = -1
			}
		}
		if ev.Type == episodic.RunState {
			_ = json.Unmarshal(ev.Payload, &owner)
		}
	}
	if start < 0 || !verificationReviewScope(events[start], sessionID, planID) {
		return nil, fmt.Errorf("plan changed or belongs to another session")
	}
	if owner.ID != runID || owner.SessionID != sessionID || (owner.RunID != "" && owner.RunID != runID) || owner.Step != index || terminalRun(owner.Status) {
		return nil, fmt.Errorf("guidance amendment no longer owns the active run/step")
	}
	available := verificationReviewEvidence(events[start+1:], sessionID, runID, planID, p)
	record := &PlanAmendment{PlanID: planID, SessionID: sessionID, RunID: runID, Index: index, Authority: "model_implementation_guidance_only", Input: in, PreviousGuidance: p.Guidance[in.StepID], OriginalStep: p.Steps[index], ContractSHA256: immutablePlanSHA(p), Impact: "Advisory implementation guidance only. No requirements, steps, checks, files, fixtures, order, budget or historical evidence changed; active/shared-file evidence must be rechecked after implementation changes."}
	seen := map[string]bool{}
	freshFailure := false
	for _, id := range in.EvidenceEventIDs {
		if !verificationReviewEventID.MatchString(id) || len(id) > 128 || seen[id] {
			return nil, fmt.Errorf("invalid or duplicate evidence reference %q", id)
		}
		seen[id] = true
		evidence, ok := available[id]
		if !ok {
			return nil, fmt.Errorf("evidence %s is not an observed result in the current plan", id)
		}
		record.Evidence = append(record.Evidence, evidence)
		if evidence.Kind == "verify_finished" && !evidence.Historical && evidence.DeclaredSourceVersion == version {
			for _, ev := range events {
				if ev.ID != id {
					continue
				}
				var payload struct {
					Index   *int     `json:"index"`
					Verdict *Verdict `json:"verdict"`
				}
				if json.Unmarshal(ev.Payload, &payload) == nil && payload.Index != nil && *payload.Index == index && payload.Verdict != nil && !payload.Verdict.Pass && reviewMatchesLatestCheck(events[start+1:], planID, sessionID, runID, index, p.Steps[index].Verify, verdictWithContainsReceipt(*payload.Verdict, p.Steps[index].Verify, id)) {
					freshFailure = true
				}
			}
		}
	}
	if !freshFailure {
		return nil, fmt.Errorf("cite the latest failed harness verify_finished event for this step and unchanged workspace; guidance is not permission to replace a check")
	}
	raw, _ := json.Marshal(record)
	record.ID = "guidance_" + recoverySHA(raw)
	return record, nil
}

func verdictWithContainsReceipt(v Verdict, check *plan.Verify, eventID string) Verdict {
	if check != nil && strings.EqualFold(strings.TrimSpace(check.Kind), "contains") && v.EvidenceEventID == "" {
		v.EvidenceEventID = eventID
	}
	return v
}

func applyPlanAmendment(s *Supervisor, amendment PlanAmendment, eventID string) {
	p := s.Plan
	if p.Guidance == nil {
		p.Guidance = map[string]string{}
	}
	amendment.EventID = eventID
	p.Guidance[amendment.Input.StepID] = amendment.Input.Guidance
	p.Revision = eventID
	p.Amendments = append(p.Amendments, amendment)
	// Guidance itself changes no executable fact. The normal step admission
	// has already invalidated shared-file evidence before any workspace write.
	// Preserve every state, failed verdict, attempt and revision counter here.
}

func projectPlanAmendment(s *Supervisor, planID, sessionID string, owner reviewReplayOwner, prior []episodic.Event, ev episodic.Event) {
	var note struct {
		Kind      string         `json:"kind"`
		Amendment *PlanAmendment `json:"amendment"`
		SessionID string         `json:"session_id"`
		RunID     string         `json:"run_id"`
	}
	if json.Unmarshal(ev.Payload, &note) != nil || note.Kind != "plan_guidance_amended" || note.Amendment == nil {
		return
	}
	a := note.Amendment
	if a.EventID != "" || a.SessionID != note.SessionID || a.RunID != note.RunID || (sessionID != "" && a.SessionID != sessionID) || a.RunID != owner.ID || a.PlanID != planID {
		return
	}
	raw, _ := json.Marshal(a.Input)
	want, err := newPlanAmendment(prior, s.Plan, a.SessionID, a.RunID, planID, a.Index, a.Input.ExpectedWorkspaceVersion, string(raw))
	if err != nil || !sameReviewJSON(want, a) {
		return
	}
	applyPlanAmendment(s, *a, ev.ID)
}

type planAdaptationDispatcher struct {
	writer                              *episodic.Writer
	sessionID, runID, planID, workspace string
	index                               int
	current                             *Plan
	applied                             *Supervisor
	stopped                             *planAmendmentLimit
}

func (t *planAdaptationDispatcher) evidencePrompt() string {
	events, err := t.writer.Events()
	if err != nil {
		return "\nNo readable amendment evidence snapshot; do not amend.\n"
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != episodic.Note {
			continue
		}
		var n struct {
			Kind      string
			Index     *int
			RunID     string `json:"run_id"`
			SessionID string `json:"session_id"`
			Verdict   *Verdict
		}
		if json.Unmarshal(events[i].Payload, &n) != nil || n.Kind != "verify_finished" || n.Index == nil || *n.Index != t.index || n.RunID != t.runID || n.SessionID != t.sessionID {
			continue
		}
		if n.Verdict != nil && !n.Verdict.Pass {
			return "\nLatest failed harness verify_finished receipt for an implementation-guidance amendment: " + events[i].ID + ". Cite it only while its workspace version is current; child tool-result receipts are separate observations.\n"
		}
		break
	}
	return "\nNo current failed harness result is available for an automatic guidance amendment.\n"
}

func (t *planAdaptationDispatcher) Name() string { return planAdaptationName }
func (t *planAdaptationDispatcher) Description() string {
	return "Version an evidence-backed model implementation assumption after a failed harness check. Advisory guidance only: original requirements, step detail, checks, assertions, fixtures, files, order and budgets are immutable. Cite the latest failed verify_finished event on unchanged workspace and actual observations. Maximum two amendments per step, six per plan; equivalent repeats require explicit review. Changes to acceptance or structure require request_verification_review, never this tool."
}
func (t *planAdaptationDispatcher) Schema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"expected_revision": map[string]any{"type": "string"}, "step_id": map[string]any{"type": "string"}, "expected_workspace_version": map[string]any{"type": "string"},
		"guidance":           map[string]any{"type": "string", "minLength": 1, "maxLength": 4000, "description": "Replacement model-owned implementation guidance, subordinate to every original requirement. No acceptance or authority changes."},
		"reason":             map[string]any{"type": "string", "minLength": 1, "maxLength": 4000, "description": "Concrete contradicted implementation assumption, cited observation and bounded impact; evidence does not prove semantic equivalence of tests."},
		"evidence_event_ids": map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "uniqueItems": true, "items": map[string]any{"type": "string"}},
	}, "required": []string{"expected_revision", "step_id", "expected_workspace_version", "guidance", "reason", "evidence_event_ids"}}
}
func (t *planAdaptationDispatcher) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	unlock := plan.LockMutation(t.sessionID)
	defer unlock()
	events, err := t.writer.Events()
	if err != nil {
		return "", err
	}
	snapshot, id, err := ReducePlan(events)
	if err != nil {
		return "", err
	}
	if id != t.planID || !samePlanSnapshot(snapshot.Plan, t.current) {
		return "", &planSnapshotError{"plan changed before guidance amendment; refresh before continuing"}
	}
	version := planAmendmentWorkspaceVersion(t.workspace, snapshot.Plan.Steps[t.index])
	amendment, err := newPlanAmendment(events, snapshot.Plan, t.sessionID, t.runID, id, t.index, version, string(args))
	if err != nil {
		if stop, ok := err.(*planAmendmentLimit); ok {
			t.stopped = stop
		}
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// The active workspace owner excludes other tool writers. Detect external
	// changes during validation too; a changed source needs fresh evidence.
	if planAmendmentWorkspaceVersion(t.workspace, snapshot.Plan.Steps[t.index]) != version {
		return "", fmt.Errorf("workspace changed during amendment validation")
	}
	ev, err := t.writer.Append(episodic.Note, map[string]any{"kind": "plan_guidance_amended", "session_id": t.sessionID, "run_id": t.runID, "plan_event_id": id, "index": t.index, "amendment": amendment})
	if err != nil {
		return "", err
	}
	applyPlanAmendment(snapshot, *amendment, ev.ID)
	t.applied = snapshot
	return fmt.Sprintf("Implementation guidance recorded as %s, revision %s. Original contract/checks and all spent budgets remain unchanged. This is not acceptance approval or semantic validation.", amendment.ID, ev.ID), nil
}
