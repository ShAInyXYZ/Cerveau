package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cerveau/internal/episodic"
)

// The model's natural output is a markdown plan. ParsePlanMarkdown turns the
// three shapes it actually produces — ## headings, numbered lists, checkboxes —
// into structured steps, so a plan never has to be hand-JSONed.
func TestParsePlanMarkdownHeadings(t *testing.T) {
	title, steps := ParsePlanMarkdown(`# 3D Chess Game

## Sprint 1: Foundation
Set up vite, `+"`src/main.ts`"+` and `+"`src/engine/ChessEngine.ts`"+`.

## Sprint 2: Rules
Implement moves and check detection.
`)
	if title != "3D Chess Game" {
		t.Errorf("title: %q", title)
	}
	if len(steps) != 2 || steps[0].Title != "Sprint 1: Foundation" {
		t.Fatalf("steps: %+v", steps)
	}
	if len(steps[0].Files) != 2 || steps[0].Files[0] != "src/main.ts" {
		t.Errorf("backticked paths should become files: %+v", steps[0].Files)
	}
	if !strings.Contains(steps[0].Detail, "vite") {
		t.Errorf("body text should become detail: %q", steps[0].Detail)
	}
}

func TestParsePlanMarkdownNumberedAndCheckbox(t *testing.T) {
	_, steps := ParsePlanMarkdown(`Plan:
1. Create the board renderer
2. Wire input handling
`)
	if len(steps) != 2 || steps[1].Title != "Wire input handling" {
		t.Fatalf("numbered: %+v", steps)
	}
	_, steps = ParsePlanMarkdown(`# Fixes
- [ ] Fix camera signature
- [ ] Delete input.update call
`)
	if len(steps) != 2 || steps[0].Title != "Fix camera signature" {
		t.Fatalf("checkbox: %+v", steps)
	}
}

// PlanLike spots a plan document; ordinary code/docs must not match.
func TestPlanLike(t *testing.T) {
	if !PlanLike("plan.md", "# Build\n## Step 1\n## Step 2\n") {
		t.Error("plan.md with steps should match")
	}
	if !PlanLike("docs/game-plan.md", "1. First\n2. Second\n3. Third\n") {
		t.Error("plan-named md with numbered steps should match")
	}
	if PlanLike("README.md", "# Project\nSome docs.\n## Install\n## Usage\n") {
		t.Error("a README is not a plan")
	}
	if PlanLike("main.go", "// plan: 1. do X 2. do Y") {
		t.Error("code files are never plans")
	}
}

// commit_plan's markdown path: a plan handed over as plain markdown must
// produce a real structured plan event.
func TestCommitPlanFromMarkdown(t *testing.T) {
	dir := t.TempDir()
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(dir + "/" + id + ".jsonl") }
	cp := NewCommitPlan(open, &SessionContext{SessionID: "s1"})

	out, err := cp.Execute(context.Background(), json.RawMessage(`{"markdown":"# Chess\n## Engine\nBuild `+"`"+`src/engine.ts`+"`"+`\n## Renderer\nBuild the board\n"}`))
	if err != nil {
		t.Fatalf("markdown plan should commit: %v", err)
	}
	if !strings.Contains(out, "2 steps") {
		t.Errorf("should commit 2 parsed steps: %q", out)
	}
}
