package loop

import (
	"strings"
	"testing"

	"cerveau/internal/plan"
)

func TestStepPromptCarriesTheCheck(t *testing.T) {
	p := StepPrompt{
		Index:  1,
		Step:   PlanStep{Title: "Car model", Detail: "boxes for body and wheels", Files: []string{"car.js"}},
		Verify: &plan.Verify{Kind: "contains", File: "car.js", Symbol: "buildCar"},
	}
	got := p.Text()
	for _, want := range []string{"STEP 2", "Car model", "car.js", "buildCar", "Do this step ONLY"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
	// The original user prompt must NOT be here — that was the old design.
	if strings.Contains(got, "committed plan:") && strings.Contains(got, "Build a") {
		t.Error("the step prompt must not carry the whole original task")
	}
}

func TestRevisionPromptSaysWhy(t *testing.T) {
	p := StepPrompt{
		Index: 0, Rev: 1,
		Step:    PlanStep{Title: "Scene"},
		Verify:  &plan.Verify{Kind: "command", Command: "node --check index.js"},
		Context: RevisionContext(2, "Physics", "needs a shared `state` object"),
	}
	got := p.Text()
	for _, want := range []string{"REVISION 1", "Step 3", "Physics", "state", "do not rewrite work that already passed"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("revision prompt missing %q:\n%s", want, got)
		}
	}
}

func TestStepContextListsOnlyVerifiedWork(t *testing.T) {
	s := NewSupervisor(plan4())
	if StepContext(s) != "" {
		t.Fatal("nothing verified yet — no context")
	}
	s.Record(0, Verdict{Pass: true, Check: "index.html contains initScene"}, -1)
	s.Record(1, Verdict{Pass: false, Check: "nope"}, -1) // failed: must NOT appear
	got := StepContext(s)
	if !strings.Contains(got, "initScene") {
		t.Errorf("passed step should appear: %s", got)
	}
	if strings.Contains(got, "Car model") {
		t.Errorf("a failed step must not be reported as ground truth: %s", got)
	}
}
