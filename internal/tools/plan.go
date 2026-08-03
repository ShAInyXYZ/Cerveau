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
	return "Commit the plan so it appears as a tracked plan card and Autopilot can execute it step by step. " +
		"EASIEST: pass your whole plan as markdown in the `markdown` field (## headings, numbered list, or checkboxes " +
		"— they become steps automatically). Or pass structured title+steps. Never write a plan to a .md file " +
		"or narrate it in prose — an uncommitted plan cannot be tracked."
}

func (t *CommitPlan) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":    map[string]any{"type": "string"},
			"markdown": map[string]any{"type": "string", "description": "the plan as markdown — ## headings, a numbered list, or checkboxes become steps; backticked file paths become each step's files"},
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
		"required": []string{},
	}
}

func (t *CommitPlan) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var plan struct {
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
		Steps    []struct {
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
	// Markdown path: the model hands over its plan as it naturally wrote it,
	// and the parser does the structuring — a small model reliably emits
	// markdown but flubs nested JSON arrays.
	if len(plan.Steps) == 0 && plan.Markdown != "" {
		mdTitle, parsed := ParsePlanMarkdown(plan.Markdown)
		if plan.Title == "" {
			plan.Title = mdTitle
		}
		for _, p := range parsed {
			plan.Steps = append(plan.Steps, struct {
				Title  string   `json:"title"`
				Detail string   `json:"detail"`
				Files  []string `json:"files"`
				Risk   string   `json:"risk"`
			}{Title: p.Title, Detail: p.Detail, Files: p.Files, Risk: p.Risk})
		}
	}
	if plan.Title == "" && len(plan.Steps) > 0 {
		plan.Title = "Plan"
	}
	if plan.Title == "" || len(plan.Steps) == 0 {
		return "", fmt.Errorf("plan needs steps — pass your plan text in the markdown field (## headings, a numbered list, or checkboxes)")
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
