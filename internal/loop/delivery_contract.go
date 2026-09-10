package loop

import (
	"encoding/json"
	"fmt"
	"strings"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
)

func validatePlanDelivery(path string, p *Plan, sessionID string) error {
	_, err := planDeliveryAdmission(path, p, sessionID)
	return err
}

// Admission is read-only. In particular, accepting an already-executed legacy
// plan must not manufacture milestone assignments or replace its root event,
// effective revision, checkpoints, attempts, or historical failed checks.
func planDeliveryAdmission(path string, p *Plan, sessionID string) (string, error) {
	events, err := episodic.Replay(path)
	if err != nil {
		return "", err
	}
	text, source := plan.PlanningRequest(events, true)
	actual, err := plan.ParseDelivery(text, source)
	if err != nil {
		return "", err
	}
	boundary := ""
	validationContract := actual
	if p.Delivery == nil {
		// An explicitly present null contract is malformed new-schema data,
		// not the absent field written by the pre-binding harness.
		if latest := latestDeliveryPlanEvent(events); latest != nil && deliveryJSONHas(*latest, "delivery_contract") {
			return "", fmt.Errorf("plan contains a missing or malformed delivery contract; explicit plan review is required")
		}
		if actual.Status == "explicit_order" {
			planID, receipt, ok := legacyDeliveryResumeEvidence(events, p, sessionID)
			if !ok {
				return "", fmt.Errorf("plan has no delivery-order binding or established legacy execution evidence; explicit plan review is required before execution")
			}
			boundary = fmt.Sprintf("Legacy delivery-order boundary: resuming saved plan %s using historical check receipt %s. Its saved step order is retained, NOT validated against the original delivery list. No milestone mapping or delivery-order waiver is inferred. Original user requirements and final acceptance remain binding; steps, checks and historical evidence are unchanged. Surface any required-order conflict for an explicit decision.", planID, receipt)
			validationContract = nil
		}
	} else if !sameReviewJSON(p.Delivery, actual) {
		return "", fmt.Errorf("delivery contract does not match the original user request; refresh the plan")
	}
	ids, milestones := map[string]bool{}, make([]string, len(p.Steps))
	for i, st := range p.Steps {
		id := plan.StepID(st.ID, i)
		if !plan.ValidStableID(id) || ids[id] {
			return "", fmt.Errorf("invalid or duplicate stable step ID")
		}
		ids[id] = true
		milestones[i] = st.MilestoneID
	}
	return boundary, plan.ValidateDelivery(validationContract, milestones)
}

func latestDeliveryPlanEvent(events []episodic.Event) *episodic.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.Plan {
			return &events[i]
		}
	}
	return nil
}

// Case-insensitive presence matches encoding/json's field binding. Even a null
// or empty modern field must not masquerade as genuinely absent legacy data.
func deliveryJSONHas(ev episodic.Event, field string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(ev.Payload, &object) != nil {
		return true
	}
	for key := range object {
		if strings.EqualFold(key, field) {
			return true
		}
	}
	return false
}

func legacyDeliveryResumeEvidence(events []episodic.Event, p *Plan, sessionID string) (string, string, bool) {
	// The caller's requested session is the provenance anchor. A copied
	// foreign observation cannot nominate its own session as the expected one.
	if sessionID == "" {
		return "", "", false
	}
	latest := latestDeliveryPlanEvent(events)
	if latest == nil {
		return "", "", false
	}
	for _, field := range []string{"delivery_contract", "revision", "implementation_guidance", "amendments"} {
		if deliveryJSONHas(*latest, field) {
			return "", "", false
		}
	}
	var original struct {
		Steps     []json.RawMessage `json:"steps"`
		SessionID string            `json:"session_id"`
	}
	if json.Unmarshal(latest.Payload, &original) != nil || len(original.Steps) == 0 {
		return "", "", false
	}
	if original.SessionID != "" && original.SessionID != sessionID {
		return "", "", false
	}
	for _, step := range original.Steps {
		if deliveryJSONHas(episodic.Event{Payload: step}, "id") || deliveryJSONHas(episodic.Event{Payload: step}, "milestone_id") {
			return "", "", false
		}
	}
	effective, id, err := ReducePlan(events)
	if err != nil || id != latest.ID || !samePlanSnapshot(effective.Plan, p) {
		return "", "", false
	}
	start := 0
	for i, ev := range events {
		if ev.ID == id && ev.Type == episodic.Plan {
			start = i + 1
			break
		}
	}
	// Generic schema_version belongs to the run envelope and existed before
	// delivery binding. The proof is the old plan shape plus its own actual
	// paired harness verification, never wall-clock dates or bare statuses.
	// Compatibility requires explicit same-session observations. The review
	// reader's broader missing-legacy-envelope tolerance is not authority to
	// establish which session owns an otherwise unscoped old plan.
	scoped := make([]episodic.Event, 0, len(events)-start)
	for _, ev := range events[start:] {
		var envelope struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(ev.Payload, &envelope) == nil && envelope.SessionID == sessionID {
			scoped = append(scoped, ev)
		}
	}
	observations := verificationReviewEvidence(scoped, sessionID, "", id, p)
	for _, ev := range events[start:] {
		if observed, ok := observations[ev.ID]; ok && observed.Kind == "verify_finished" && observed.RunID != "" {
			return id, ev.ID, true
		}
	}
	return "", "", false
}

func effectivePlanRevision(planID string, p *Plan) string {
	if p.Revision != "" {
		return p.Revision
	}
	return planID
}

// This content hash matches verifyStep's before/after declared-source receipt,
// not its separate metadata-only WorkspaceVersion fingerprint.
func planAmendmentWorkspaceVersion(workspace string, step PlanStep) string {
	return recoveryStepVersion(workspace, step)
}

func effectiveStepPrompt(sp StepPrompt, p *Plan, index int, planID, workspace string) string {
	text := sp.Text()
	id := plan.StepID(p.Steps[index].ID, index)
	text += fmt.Sprintf("\nPlan amendment scope: plan_event_id=%s; expected_revision=%s; step_id=%s; expected_workspace_version=%s (declared-source content version, not the metadata fingerprint).\n", planID, effectivePlanRevision(planID, p), id, planAmendmentWorkspaceVersion(workspace, p.Steps[index]))
	if p.Delivery != nil && p.Delivery.Status == "explicit_order" {
		for _, milestone := range p.Delivery.Milestones {
			if milestone.ID == p.Steps[index].MilestoneID {
				text += "Original user delivery milestone (binding; must be achieved before later milestones):\n" + milestone.Text + "\n"
			}
		}
	} else if p.Delivery == nil {
		text += "Legacy delivery-order boundary: this saved plan has no milestone binding. Its saved order is retained, not validated against any explicit delivery list in the original request. No delivery-order waiver is inferred. Original user requirements and final acceptance remain binding; surface conflicts for an explicit decision.\n"
	} else {
		text += "Delivery-order semantics in ordinary prose are not mechanically validated; the original request remains binding.\n"
	}
	text += "You may use amend_implementation_guidance for an evidence-backed implementation assumption only. It cannot change this step's requirements, files, checks, fixtures, acceptance, authority or delivery order. A conflict in those requires request_verification_review and an explicit decision. Amendments retain spent budgets and failed evidence.\n"
	if guidance := p.Guidance[id]; guidance != "" {
		text += "Current model-owned implementation guidance (advisory, not acceptance or authority; original contract wins any conflict):\n" + guidance + "\n"
	}
	return text
}
