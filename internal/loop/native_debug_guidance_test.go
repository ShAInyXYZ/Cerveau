package loop

import (
	"strings"
	"testing"
)

func TestRecoveryNativeDebugGuidancePreservesVerification(t *testing.T) {
	s := NewSupervisor(&Plan{Steps: []PlanStep{{Title: "repair fixture"}}})
	s.Steps[0].Verdict = &Verdict{Pass: false, Check: "committed predicate", Evidence: "source failure"}
	before := *s.Steps[0].Verdict
	got := recoveryInstructions(s, 0)
	for _, want := range []string{"code_diagnostics", "run_checks", "browser_run", "runtime_profile", "not permission to replace the committed check", "do not prove application correctness", "Missing dependencies or unsupported checks remain unverified", "script_args", "same-session", "historical, never a current pass"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing bounded recovery guidance %q", want)
		}
	}
	if s.Steps[0].Verdict.Pass || s.Steps[0].Verdict.Check != before.Check || s.Steps[0].Verdict.Evidence != before.Evidence {
		t.Fatal("guidance changed the committed verification state")
	}
}
