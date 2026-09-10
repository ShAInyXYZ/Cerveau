package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cerveau/internal/episodic"
	cplan "cerveau/internal/plan"
)

// planStepIn is one step as the model sends it.
//
// Verify is what turns a plan from a description into something executable: the
// observation that proves the step done. Without it, "done" is inferred from
// whether the step's files exist, which cannot say which step wrote a file and
// can never say a verification passed — the inference that reported 4/4 green
// on the car run while the turn was dying in a check_page loop.
type planStepIn struct {
	ID          string          `json:"id,omitempty"`
	MilestoneID string          `json:"milestone_id,omitempty"`
	Title       string          `json:"title"`
	Detail      string          `json:"detail"`
	Files       []string        `json:"files"`
	Risk        string          `json:"risk"`
	Verify      json.RawMessage `json:"verify,omitempty"`
}

type CommitPlan struct {
	open func(sessionID string) (*episodic.Writer, error)
	sctx *SessionContext
}

func NewCommitPlan(open func(string) (*episodic.Writer, error), sctx *SessionContext) *CommitPlan {
	return &CommitPlan{open: open, sctx: sctx}
}

func (t *CommitPlan) Name() string { return "commit_plan" }

func (t *CommitPlan) Description() string {
	return "Commit the plan so it appears as a tracked plan card and Autopilot can execute it step by step. " +
		"Pass structured steps; every step must carry a " +
		"`verify` — the check that proves the step done, which is what lets Autopilot run and confirm one step " +
		"at a time instead of guessing from which files exist. A verify must be able to FAIL: a check_page eval, " +
		"a command's exit code, or a file that must contain a named symbol. \"The file exists\" is not a check. " +
		"The verify must cover all behavior claimed in the step's title and detail; " +
		"use a focused assertion suite or split the step if one check cannot cover it. " +
		"Syntax or symbol checks alone do not prove runtime behavior. Test each step's behavior " +
		"there; reserve later verification steps for integration and browser checks. " +
		"Preserve the user's explicit delivery order and early runnable/observable milestones. " +
		"Do not replace an early playable or browser-tested slice with a subsystem-first plan that postpones it. " +
		"Before committing, compare the proposed order and checks against the original request; " +
		"surface genuine prerequisite conflicts rather than silently reordering required milestones. " +
		"Never write a plan to a .md file or narrate it in prose — an uncommitted plan cannot be tracked."
}

func (t *CommitPlan) Schema() map[string]any {
	verify := cplan.VerifySchema()
	verify["description"] = "REQUIRED. A runnable check that can FAIL and covers all behavior claimed in the step's title and detail. " +
		"Use a focused assertion suite or split the step; syntax, symbols and file existence alone do not prove runtime behavior."
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":    map[string]any{"type": "string"},
			"markdown": map[string]any{"type": "string", "description": "the plan as markdown — ## headings, a numbered list, or checkboxes become steps; backticked file paths become each step's files"},
			"steps": map[string]any{
				"type": "array",
				// Under a FORCED tool_choice the decoder generates inside this
				// schema, and an empty array was a legal exit: the model
				// produced {"title": …, "steps": []} twice in five seconds and
				// the gate gave up (2026-09-04). At least one step, always.
				"minItems": 1,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":           map[string]any{"type": "string", "description": "Optional unique stable step ID; generated when absent."},
						"milestone_id": map[string]any{"type": "string", "description": "Required when the user supplied an explicit Delivery order/Delivery milestones numbered list: milestone-1, milestone-2, etc. (or the user's structured contract IDs). Cover every milestone in original order. Membership is not semantic proof; each check must prove the named user milestone."},
						"title":        map[string]any{"type": "string"},
						"detail":       map[string]any{"type": "string", "description": "Behavior and invariants for this milestone. For shared state, specify who creates, updates and removes it, and check repeated operations as well as first initialization. Preserve explicitly requested delivery order."},
						"files": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"},
							"description": "the file(s) this step creates or changes — REQUIRED; a later revision re-checks every step that shares one"},
						"risk":   map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
						"verify": verify,
					},
					"required": []string{"title", "files", "verify"},
				},
			},
			"autonomy_budget": map[string]any{
				"type":        "string",
				"enum":        []string{"low", "high"},
				"description": "Autonomy preference. Failures remain blocking; higher autonomy never permits skipping a failed check or overriding user constraints.",
			},
		},
		// title + steps required. The markdown-only shape still works on an
		// ordinary call — the schema is advisory there — but a forced call is
		// decoded inside it, and there the plan must be structured.
		"required": []string{"title", "steps"},
	}
}

func (t *CommitPlan) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var plan struct {
		Title          string       `json:"title"`
		Markdown       string       `json:"markdown"`
		Steps          []planStepIn `json:"steps"`
		AutonomyBudget string       `json:"autonomy_budget"`
	}
	if err := json.Unmarshal(args, &plan); err != nil {
		return "", fmt.Errorf("bad plan: %w", err)
	}
	// Markdown path: the model hands over its plan as it naturally wrote it,
	// and the parser does the structuring — a small model reliably emits
	// markdown but flubs nested JSON arrays.
	if len(plan.Steps) == 0 && plan.Markdown != "" {
		mdTitle, parsed := ParsePlanMarkdown(plan.Markdown)
		if plan.Title == "" {
			plan.Title = mdTitle
		}
		for _, p := range parsed {
			plan.Steps = append(plan.Steps, planStepIn{Title: p.Title, Detail: p.Detail, Files: p.Files, Risk: p.Risk})
		}
	}
	if plan.Title == "" && len(plan.Steps) > 0 {
		plan.Title = "Plan"
	}
	if plan.Title == "" || len(plan.Steps) == 0 {
		return "", fmt.Errorf("plan needs at least one step in `steps` (title, files, verify) — an empty steps array commits nothing")
	}
	// Every step must declare a check that can FAIL, and it is rejected here —
	// at commit time, the way a prose plan is rejected — because a criterion
	// decides the step's fate and a bad one cannot be caught later.
	//
	// Markdown plans are exempt: that path exists so a small model can hand
	// over the plan as it naturally wrote it, and demanding structured verifies
	// through it would put the easy path out of reach. Those steps fall back to
	// the old disk reconciliation, which is now honest about its limits.
	{
		for i, st := range plan.Steps {
			if strings.TrimSpace(st.Title) == "" || len(st.Files) == 0 {
				return "", fmt.Errorf("step %d needs a title and declared files", i+1)
			}
			v, err := cplan.UnmarshalVerify(st.Verify)
			if err != nil {
				return "", fmt.Errorf("step %d (%s): %w", i+1, st.Title, err)
			}
			if err := v.Validate(); err != nil {
				return "", fmt.Errorf("step %d (%s): %w", i+1, st.Title, err)
			}
		}
	}
	if plan.AutonomyBudget == "" {
		plan.AutonomyBudget = "low"
	}
	sid := SessionOf(ctx, t.sctx)
	if sid == "" {
		return "", fmt.Errorf("no active session")
	}
	unlock := cplan.LockMutation(sid)
	defer unlock()
	wr, err := t.open(sid)
	if err != nil {
		return "", err
	}
	events, err := wr.Events()
	if err != nil {
		return "", err
	}
	request, sourceID := cplan.PlanningRequest(events, false)
	delivery, err := cplan.ParseDelivery(request, sourceID)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	milestones := make([]string, len(plan.Steps))
	for i := range plan.Steps {
		st := &plan.Steps[i]
		st.ID = cplan.StepID(st.ID, i)
		if !cplan.ValidStableID(st.ID) || seen[st.ID] {
			return "", fmt.Errorf("step %d has invalid or duplicate stable id", i+1)
		}
		seen[st.ID] = true
		milestones[i] = st.MilestoneID
	}
	if err := cplan.ValidateDelivery(delivery, milestones); err != nil {
		return "", err
	}
	ev, err := wr.Append(episodic.Plan, map[string]any{
		"title": plan.Title, "steps": plan.Steps, "autonomy_budget": plan.AutonomyBudget, "delivery_contract": delivery,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("plan committed as %s (%d steps, autonomy %s; delivery order: %s, semantic coverage not validated) — ready for Autopilot", ev.ID, len(plan.Steps), plan.AutonomyBudget, delivery.Status), nil
}
