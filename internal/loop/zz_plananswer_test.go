package loop

import "testing"

// The plan gate asks the first call of an autopilot turn for a plan. Not every
// turn is a build: "who was president in 1950" has an answer, not a plan, and
// the gate used to throw that answer away and hand back the model's third-round
// "no plan to commit" instead.
func TestAnswersWithoutAPlan(t *testing.T) {
	answers := []string{
		"Harry S. Truman. He was president from 1945 until January 20, 1953, when Dwight D. Eisenhower took office.",
		"x^10 = e → x = e^(1/10) ≈ 1.10517",
		"The file is 3,412 bytes.",
		"Yes — the config already sets it.",
	}
	for _, a := range answers {
		if !answersWithoutAPlan(a) {
			t.Errorf("should be treated as an answer: %q", a)
		}
	}

	notAnswers := []string{
		"", // a tool call the parser swallowed
		"Here is the plan:\n1. build the shell\n2. add physics",
		"Step 1: create index.html. Step 2: add the car.",
		"I will start by creating the scene, then add the car model.",
		"1. Scene setup\n2. Fan geometry\n3. Controls",
		"<commit_plan><steps><step title=\"a\"/></steps></commit_plan>",
		// the gate's own question coming back must never become the reply
		"No plan to commit — the recent messages were simple Q&A, both already answered.",
		"Nothing to plan here.",
	}
	for _, n := range notAnswers {
		if answersWithoutAPlan(n) {
			t.Errorf("should NOT end the turn as an answer: %q", n)
		}
	}
}

// A wrong "yes" ends a build turn after one call with no work done, so the
// predicate must stay conservative about anything long.
func TestLongProseIsLeftToTheTranslator(t *testing.T) {
	long := ""
	for len(long) < 950 {
		long += "This is a detailed description of the work to be carried out. "
	}
	if answersWithoutAPlan(long) {
		t.Error("long prose may be a plan; the translator should get it")
	}
}
