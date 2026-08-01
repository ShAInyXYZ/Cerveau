package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"cerveau/internal/episodic"
)

type CommitPlan struct {
	open func(sessionID string) (*episodic.Writer, error)
	sctx *SessionContext
}

func NewCommitPlan(open func(string) (*episodic.Writer, error), sctx *SessionContext) *CommitPlan {
	return &CommitPlan{open: open, sctx: sctx}
}

func (t *CommitPlan) Name() string { return "commit_plan" }

func (t *CommitPlan) Description() string {
	return "Crystallize the agreed plan into a structured artifact stored in the session log. Autopilot executes it later."
}

func (t *CommitPlan) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string"},
			"steps": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":  map[string]any{"type": "string"},
						"detail": map[string]any{"type": "string"},
						"files":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"risk":   map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
					},
					"required": []string{"title"},
				},
			},
			"autonomy_budget": map[string]any{
				"type":        "string",
				"enum":        []string{"low", "high"},
				"description": "low: hand back on any step failure. high: log and continue.",
			},
		},
		"required": []string{"title", "steps"},
	}
}

func (t *CommitPlan) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var plan struct {
		Title string `json:"title"`
		Steps []struct {
			Title  string   `json:"title"`
			Detail string   `json:"detail"`
			Files  []string `json:"files"`
			Risk   string   `json:"risk"`
		} `json:"steps"`
		AutonomyBudget string `json:"autonomy_budget"`
	}
	if err := json.Unmarshal(args, &plan); err != nil {
		return "", fmt.Errorf("bad plan: %w", err)
	}
	if plan.Title == "" || len(plan.Steps) == 0 {
		return "", fmt.Errorf("plan needs a title and at least one step")
	}
	if plan.AutonomyBudget == "" {
		plan.AutonomyBudget = "low"
	}
	if t.sctx == nil || t.sctx.SessionID == "" {
		return "", fmt.Errorf("no active session")
	}
	wr, err := t.open(t.sctx.SessionID)
	if err != nil {
		return "", err
	}
	ev, err := wr.Append(episodic.Plan, map[string]any{
		"title": plan.Title, "steps": plan.Steps, "autonomy_budget": plan.AutonomyBudget,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("plan committed as %s (%d steps, autonomy %s) — ready for Autopilot", ev.ID, len(plan.Steps), plan.AutonomyBudget), nil
}
