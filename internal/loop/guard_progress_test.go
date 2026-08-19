package loop

import "testing"

// A turn that is MAKING PROGRESS must not be killed by errors it recovered
// from. Every vLLM benchmark session ended on guard_error_threshold AFTER
// producing a working artifact: the model wrote the file, hit three failures
// while self-verifying, and the guard stopped a turn that had already
// succeeded.
//
// Qwen's harness makes the same observation: productive turns issue many
// DISTINCT calls, stuck turns repeat one. So a success must forgive past
// errors rather than letting them accumulate for the life of the turn.
func TestSuccessForgivesEarlierErrors(t *testing.T) {
	g := newTurnGuardBudget(40, 100000)

	// two failures, then the model fixes it and the tool succeeds
	for i := 0; i < 2; i++ {
		if _, stop := g.toolError("bash"); stop {
			t.Fatalf("stopped after %d errors — far too eager", i+1)
		}
	}
	g.toolOK()

	// a later, unrelated failure must not be the "third strike"
	if _, stop := g.toolError("bash"); stop {
		t.Error("a single failure after a success stopped the turn — " +
			"errors are accumulating across recoveries")
	}
}

// A genuinely stuck turn — same tool failing repeatedly with no success
// between — must still be stopped.
func TestUnrecoveredFailuresStillStop(t *testing.T) {
	g := newTurnGuardBudget(40, 100000)
	var stopped bool
	for i := 0; i < 4; i++ {
		if _, s := g.toolError("bash"); s {
			stopped = true
			break
		}
	}
	if !stopped {
		t.Error("four consecutive failures with no recovery should stop the turn")
	}
}
