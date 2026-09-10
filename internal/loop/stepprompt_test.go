package loop

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"cerveau/internal/plan"
)

func TestStepPromptPreservesFullVerification(t *testing.T) {
	for _, v := range []*plan.Verify{
		{Kind: "command", Command: "node --input-type=module -e \"" + strings.Repeat("/* padding */", 15) + "if (!state.persisted) throw Error('persistence failed')\""},
		{Kind: "eval", URL: "http://127.0.0.1:8000/index.html", Expr: strings.Repeat("/* padding */", 15) + "\nwindow.__state.correct === true"},
		{Kind: "contains", File: "state.js", Symbol: "export function restoreEdits"},
	} {
		t.Run(v.Kind, func(t *testing.T) {
			p := StepPrompt{Step: PlanStep{Title: "Persist state"}, Verify: v}
			want, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if got := p.Text(); !strings.Contains(got, string(want)) {
				t.Fatalf("step prompt lost or abbreviated the exact verification contract:\n%s", got)
			}
		})
	}
}

func TestStepPromptDoesNotEquateNarrowCheckWithFullCompletion(t *testing.T) {
	p := StepPrompt{
		Step:   PlanStep{Title: "Storage and lifecycle", Detail: "Restore edits after unloading and reloading."},
		Verify: &plan.Verify{Kind: "contains", File: "state.js", Symbol: "createState"},
	}
	got := p.Text()
	for _, want := range []string{"all behavior in this step's title and detail", "only what it actually checks", "focused checks", "unverified"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing verification coverage guidance %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "This step is DONE when this check passes") {
		t.Fatal("one narrow check must not be described as sufficient proof of every claimed behavior")
	}
}

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
	if !strings.Contains(got, "passed their declared checks") || !strings.Contains(got, "untested behavior") {
		t.Errorf("context must distinguish check results from proof of all behavior: %s", got)
	}
}

func TestStepContextRetainsInvalidatedSharedChecksOnBothSides(t *testing.T) {
	p := &Plan{Steps: []PlanStep{
		{Title: "Storage", Files: []string{"world.js"}},
		{Title: "Repair mesh", Files: []string{"world.js"}},
		{Title: "Physics", Files: []string{"world.js", "physics-tests.mjs"}},
	}}
	s := NewSupervisor(p)
	s.Record(0, Verdict{Pass: true, Check: "storage check", Evidence: "OK 32768", EvidenceEventID: "evt_000011"}, -1)
	s.Record(2, Verdict{Pass: true, Check: "feet at y=1", Evidence: "OK grounded", EvidenceEventID: "evt_000022"}, -1)
	invalidateSharedEvidence(s, 1)
	got := StepContext(s)
	for _, want := range []string{"1. Storage", "3. Physics", "world.js", "physics-tests.mjs", "storage check", "feet at y=1", "OK grounded", "evt_000011", "evt_000022", "needs_reverify", "previously passed; awaiting recheck", "not current verification", "read_plan_step", "Preserve related checks"} {
		if !strings.Contains(got, want) {
			t.Errorf("context dropped prior shared-file evidence %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, " — verified:") || strings.Contains(got, "Earlier steps") {
		t.Fatalf("stale or downstream checks mislabeled:\n%s", got)
	}
}

func TestStepContextDoesNotPromoteMissingOrFailedVerdicts(t *testing.T) {
	s := NewSupervisor(&Plan{Steps: []PlanStep{{Title: "Missing verdict"}, {Title: "Failed verdict"}, {Title: "Stale failed verdict"}}})
	s.Steps[0].Status = "passed"
	s.Steps[1].Status, s.Steps[1].Verdict = "passed", &Verdict{Pass: false, Check: "not proof"}
	s.Steps[2].Status, s.Steps[2].Verdict = "needs_reverify", &Verdict{Pass: false, Check: "not a previous pass"}
	if got := StepContext(s); got != "" {
		t.Fatalf("missing or failed evidence presented as previous passing work:\n%s", got)
	}
}

func TestStepContextBoundedWithExactReadBackInstructions(t *testing.T) {
	p := &Plan{}
	for i := 0; i < 80; i++ {
		p.Steps = append(p.Steps, PlanStep{Title: strings.Repeat("源", 1000), Files: []string{strings.Repeat("源", 1000) + ".js"}})
	}
	s := NewSupervisor(p)
	for i := range s.Steps {
		s.Steps[i].Status = "needs_reverify"
		s.Steps[i].Verdict = &Verdict{Pass: true, Check: strings.Repeat("check", 1000), Evidence: strings.Repeat("証", 2000), EvidenceEventID: "evt_000123"}
	}
	got := StepContext(s)
	if len(got) > maxStepContextBytes || !utf8.ValidString(got) {
		t.Fatalf("context must stay within %d bytes and preserve UTF-8; got %d", maxStepContextBytes, len(got))
	}
	for _, want := range []string{"summary", "omitted", "read_plan_step", "zero-based index", "exact", "Preserve related checks"} {
		if !strings.Contains(got, want) {
			t.Errorf("bounded context missing %q", want)
		}
	}
}

func TestStepPromptRoutesCriterionConflictsToExplicitReview(t *testing.T) {
	got := (StepPrompt{Step: PlanStep{Title: "Meshing"}, Verify: &plan.Verify{Kind: "command", Command: "node tests.mjs"}}).Text()
	for _, want := range []string{"request_verification_review", "model-generated criterion", "original user constraints", "fixtures or metrics", "arbitrary counts", "silently relax", "read_plan_step"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing criterion-integrity guidance %q:\n%s", want, got)
		}
	}
}
