package loop

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/plan"
)

const (
	verificationReviewName      = "request_verification_review"
	verificationReviewArgsLimit = 24000
	verificationReviewReasonMax = 4000
	verificationReviewRefsMax   = 8
	verificationReviewVerifyMax = 16000
)

var verificationReviewEventID = regexp.MustCompile(`^evt_[0-9]{6,}$`)

// VerificationReview records a model's concern, not a finding that a check is
// wrong. Neither recording it nor retrying the step authorizes a new criterion.
// OriginalVerify is copied from the current committed plan, never model input.
type VerificationReview struct {
	ProposalID              string                       `json:"proposal_id"`
	EventID                 string                       `json:"event_id,omitempty"`
	PlanID                  string                       `json:"plan_event_id"`
	Index                   int                          `json:"index"`
	SessionID               string                       `json:"session_id"`
	RunID                   string                       `json:"run_id"`
	Status                  string                       `json:"status"`
	Reason                  string                       `json:"reason"`
	OriginalVerify          *plan.Verify                 `json:"original_verify"`
	OriginalCheckSHA256     string                       `json:"original_check_sha256"`
	ProposedVerify          *plan.Verify                 `json:"proposed_verify,omitempty"`
	ProposedCheckSHA256     string                       `json:"proposed_check_sha256,omitempty"`
	ProposedVerifyStatus    string                       `json:"proposed_verify_status,omitempty"`
	EvidenceEventIDs        []string                     `json:"evidence_event_ids"`
	Evidence                []VerificationReviewEvidence `json:"evidence"`
	CurrentEvidenceEventID  string                       `json:"current_evidence_event_id,omitempty"`
	CurrentWorkspaceVersion string                       `json:"current_workspace_version,omitempty"`
	CurrentCheckPass        bool                         `json:"current_check_pass"`
}

type VerificationReviewEvidence struct {
	EventID               string `json:"event_id"`
	PayloadSHA256         string `json:"payload_sha256"`
	RunID                 string `json:"run_id,omitempty"`
	Historical            bool   `json:"historical"`
	Kind                  string `json:"kind"`
	Tool                  string `json:"tool,omitempty"`
	Excerpt               string `json:"excerpt"`
	WorkspaceVersion      string `json:"workspace_version,omitempty"`
	DeclaredSourceVersion string `json:"declared_source_version,omitempty"`
}

type verificationReviewRequest struct {
	Review *VerificationReview
}

func (r *verificationReviewRequest) Error() string {
	return "Verification review requested; the committed check and acceptance requirements are unchanged. Human review required: " + r.Review.Reason
}

func verificationReviewTool() llm.ToolSpec {
	verify := plan.VerifySchema()
	verify["additionalProperties"] = false
	return llm.ToolSpec{Type: "function", Function: llm.FunctionSpec{
		Name:        verificationReviewName,
		Description: "Stop the current step for human review when observed evidence suggests its committed verification contradicts the task or fixture. Cite actual tool-result or verify-finished event IDs from this session's current plan. An optional replacement is a proposal only: it is not executed, validated for correctness, approved, or installed. The unchanged committed check still determines the verdict. Do not weaken assertions or alter acceptance tests.",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"reason": map[string]any{"type": "string", "minLength": 1, "maxLength": verificationReviewReasonMax,
					"description": "The concrete contradiction, its evidence, and why an implementation repair cannot satisfy the unchanged requirement."},
				"evidence_event_ids": map[string]any{"type": "array", "minItems": 1, "maxItems": verificationReviewRefsMax, "uniqueItems": true,
					"items": map[string]any{"type": "string", "pattern": verificationReviewEventID.String(), "maxLength": 128}},
				"proposed_verify": verify,
			},
			"required": []string{"reason", "evidence_event_ids"},
		},
	}}
}

// newVerificationReview is deliberately pure: it receives no registry or
// executor and cannot run a proposed command or mutate a plan/workspace.
// events must be replayed from the current session's actual journal by caller.
func newVerificationReview(events []episodic.Event, sessionID, runID, planID string, idx int, original *plan.Verify, args string) (*verificationReviewRequest, error) {
	if sessionID == "" || runID == "" || planID == "" || idx < 0 {
		return nil, fmt.Errorf("verification review requires the active session, run, plan and step")
	}
	if len(args) > verificationReviewArgsLimit {
		return nil, fmt.Errorf("verification review arguments exceed %d bytes", verificationReviewArgsLimit)
	}
	var input struct {
		Reason           string          `json:"reason"`
		EvidenceEventIDs []string        `json:"evidence_event_ids"`
		ProposedVerify   json.RawMessage `json:"proposed_verify"`
	}
	if err := decodeVerificationReviewJSON(args, &input); err != nil {
		return nil, fmt.Errorf("invalid verification review: %w", err)
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || len(input.Reason) > verificationReviewReasonMax {
		return nil, fmt.Errorf("verification review reason must contain 1 to %d bytes", verificationReviewReasonMax)
	}
	if len(input.EvidenceEventIDs) < 1 || len(input.EvidenceEventIDs) > verificationReviewRefsMax {
		return nil, fmt.Errorf("verification review needs 1 to %d evidence event IDs", verificationReviewRefsMax)
	}
	start := -1
	var committed Plan
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != episodic.Plan {
			continue
		}
		if events[i].ID != planID || !verificationReviewScope(events[i], sessionID, planID) {
			return nil, fmt.Errorf("verification review plan changed or belongs to another session")
		}
		if err := json.Unmarshal(events[i].Payload, &committed); err != nil {
			return nil, fmt.Errorf("invalid committed plan: %w", err)
		}
		start = i + 1
		break
	}
	if start < 0 || idx >= len(committed.Steps) || original == nil {
		return nil, fmt.Errorf("verification review has no matching committed step")
	}
	originalRaw, _ := json.Marshal(original)
	committedRaw, _ := json.Marshal(committed.Steps[idx].Verify)
	if string(originalRaw) != string(committedRaw) || original.Validate() != nil {
		return nil, fmt.Errorf("verification review original check does not match the current committed step")
	}
	originalCopy := *original
	review := &VerificationReview{
		PlanID: planID, Index: idx, SessionID: sessionID, RunID: runID,
		Status: "human_review_required", Reason: input.Reason,
		OriginalVerify: &originalCopy, OriginalCheckSHA256: recoverySHA(originalRaw),
		EvidenceEventIDs: append([]string(nil), input.EvidenceEventIDs...),
	}
	if len(input.ProposedVerify) > 0 && string(input.ProposedVerify) != "null" {
		if len(input.ProposedVerify) > verificationReviewVerifyMax {
			return nil, fmt.Errorf("proposed verification exceeds %d bytes", verificationReviewVerifyMax)
		}
		var proposed plan.Verify
		if err := decodeVerificationReviewJSON(string(input.ProposedVerify), &proposed); err != nil {
			return nil, fmt.Errorf("invalid proposed verification: %w", err)
		}
		if err := proposed.Validate(); err != nil {
			return nil, fmt.Errorf("invalid proposed verification shape: %w", err)
		}
		raw, _ := json.Marshal(proposed)
		review.ProposedVerify, review.ProposedCheckSHA256 = &proposed, recoverySHA(raw)
		review.ProposedVerifyStatus = "unvalidated_not_authorized"
	}
	available := verificationReviewEvidence(events[start:], sessionID, runID, planID, &committed)
	seen := map[string]bool{}
	for _, id := range input.EvidenceEventIDs {
		if len(id) > 128 || !verificationReviewEventID.MatchString(id) || seen[id] {
			return nil, fmt.Errorf("invalid or duplicate verification evidence event ID %q", id)
		}
		seen[id] = true
		evidence, ok := available[id]
		if !ok {
			return nil, fmt.Errorf("evidence %s is not a paired tool observation or harness verification from this session's current plan", id)
		}
		review.Evidence = append(review.Evidence, evidence)
	}
	return &verificationReviewRequest{Review: review}, nil
}

func decodeVerificationReviewJSON(raw string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}

// Missing legacy envelope fields may be accepted only because the caller reads
// the actual session journal; explicit foreign identities always fail closed.
func verificationReviewScope(ev episodic.Event, sessionID, planID string) bool {
	var scope struct {
		SessionID string `json:"session_id"`
		PlanID    string `json:"plan_event_id"`
	}
	return json.Unmarshal(ev.Payload, &scope) == nil &&
		(scope.SessionID == "" || scope.SessionID == sessionID) &&
		(scope.PlanID == "" || scope.PlanID == planID)
}

func verificationReviewEvidence(events []episodic.Event, sessionID, runID, planID string, committed *Plan) map[string]VerificationReviewEvidence {
	observations := map[string]VerificationReviewEvidence{}
	pending := map[string]int{}
	ambiguous := map[string]bool{}
	checks := map[string]string{}
	for _, ev := range events {
		if !verificationReviewScope(ev, sessionID, planID) {
			continue
		}
		var payload struct {
			ID, Name, Output, Kind string
			RunID                  string `json:"run_id"`
			OK                     *bool
			Index                  *int
			Verify                 *plan.Verify
			Verdict                *Verdict
		}
		if json.Unmarshal(ev.Payload, &payload) != nil {
			continue
		}
		evidence := VerificationReviewEvidence{EventID: ev.ID, PayloadSHA256: recoverySHA(ev.Payload), RunID: payload.RunID,
			Historical: payload.RunID == "" || payload.RunID != runID}
		switch ev.Type {
		case episodic.ToolCall, episodic.ToolResult:
			if payload.ID == "" || payload.Name == "" || payload.Name == verificationReviewName || payload.Name == planAdaptationName || payload.Name == "read_plan_step" || payload.Name == "request_revision" || payload.Name == "ask_user" {
				continue // control acknowledgements and user/model prose are not observations
			}
			key := payload.RunID + "\x00" + payload.Name + "\x00" + payload.ID
			if ev.Type == episodic.ToolCall {
				pending[key]++
				if pending[key] > 1 {
					ambiguous[key] = true
				}
				continue
			}
			paired := pending[key] == 1 && !ambiguous[key]
			if pending[key] > 1 {
				pending[key]--
			} else {
				delete(pending, key)
				delete(ambiguous, key)
			}
			if !paired || payload.OK == nil || strings.TrimSpace(payload.Output) == "" {
				continue
			}
			evidence.Kind, evidence.Tool, evidence.Excerpt = "tool_result", payload.Name, recoveryExcerpt(payload.Output, 1200)
			observations[ev.ID] = evidence
		case episodic.Note:
			if payload.Index == nil || *payload.Index < 0 || *payload.Index >= len(committed.Steps) {
				continue
			}
			v := committed.Steps[*payload.Index].Verify
			if v == nil {
				continue
			}
			key := fmt.Sprintf("%s\x00%d", payload.RunID, *payload.Index)
			if payload.Kind == "verify_started" {
				raw, _ := json.Marshal(payload.Verify)
				want, _ := json.Marshal(v)
				delete(checks, key)
				if string(raw) == string(want) {
					checks[key] = recoverySHA(want)
				}
				continue
			}
			if payload.Kind != "verify_finished" || checks[key] == "" {
				continue
			}
			delete(checks, key)
			if payload.Verdict == nil || payload.Verdict.Check != v.Describe() || strings.TrimSpace(payload.Verdict.Evidence) == "" {
				continue
			}
			if ref := payload.Verdict.EvidenceEventID; ref != "" {
				if observed, ok := observations[ref]; !ok || observed.RunID != payload.RunID {
					continue
				}
			} else if !strings.EqualFold(strings.TrimSpace(v.Kind), "contains") {
				continue
			}
			evidence.Kind, evidence.Excerpt = "verify_finished", recoveryExcerpt(payload.Verdict.Evidence, 1200)
			evidence.WorkspaceVersion = payload.Verdict.WorkspaceVersion
			evidence.DeclaredSourceVersion = payload.Verdict.DeclaredSourceVersion
			observations[ev.ID] = evidence
		}
	}
	return observations
}

// persistVerificationReview attaches the freshest unchanged check result only
// after the caller has run it. The proposal itself is never an execution input.
func persistVerificationReview(wr *episodic.Writer, request *verificationReviewRequest, current Verdict) error {
	if wr == nil || request == nil || request.Review == nil {
		return fmt.Errorf("verification review has no writer or validated request")
	}
	review := request.Review
	if review.EventID != "" {
		return fmt.Errorf("verification review was already recorded")
	}
	originalRaw, _ := json.Marshal(review.OriginalVerify)
	if review.OriginalVerify == nil || review.OriginalCheckSHA256 != recoverySHA(originalRaw) || current.Check != review.OriginalVerify.Describe() {
		return fmt.Errorf("verification review requires the unchanged committed check result")
	}
	// A Verdict may already have this review attached by the caller. Persist
	// the fresh observation without recursively embedding the proposal itself.
	current.VerificationReview = nil
	review.CurrentEvidenceEventID, review.CurrentWorkspaceVersion, review.CurrentCheckPass = current.EvidenceEventID, current.WorkspaceVersion, current.Pass
	review.ProposalID = ""
	raw, _ := json.Marshal(review)
	review.ProposalID = "verification_review_" + recoverySHA(raw)
	ev, err := wr.Append(episodic.Note, map[string]any{"kind": "verification_review_requested", "index": review.Index,
		"plan_event_id": review.PlanID, "session_id": review.SessionID, "run_id": review.RunID, "review": review, "verdict": current,
		"text": "Human review required. The original verification and acceptance requirements are unchanged; the proposed verification is unvalidated and not authorized."})
	if err != nil {
		return err
	}
	review.EventID = ev.ID
	return nil
}
