package loop

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"time"
)

const (
	StopFinalAnswer = "final_answer"
	StopIterations  = "guard_iterations"
	StopTime        = "guard_time"
	StopTokens      = "guard_tokens"
	StopLoop        = "guard_loop_detected"
	StopErrors      = "guard_error_threshold"
	StopLLMError    = "llm_error"
)

type turnGuard struct {
	maxIter     int
	deadline    time.Time
	maxTokens   int
	repeatLimit int

	tokens      int
	perToolErrs map[string]int
	totalErrs   int
	budget      time.Duration
	seen        map[[20]byte]int // count of (name+args+result) triples seen
}

func newTurnGuard(maxIter int) *turnGuard { return newTurnGuardBudget(maxIter, maxTurnTime) }

// newTurnGuardBudget allows a longer wall-clock budget for turns that are
// KNOWN to be long-running — a plan step posted by a supervisor panel is a
// build task, not a chat reply, and the conversational budget starves it.
func newTurnGuardBudget(maxIter int, budget time.Duration) *turnGuard {
	if maxIter <= 0 {
		maxIter = maxIterations
	}
	if budget <= 0 {
		budget = maxTurnTime
	}
	return &turnGuard{
		budget:      budget,
		maxIter:     maxIter,
		deadline:    time.Now().Add(budget),
		maxTokens:   maxTurnTokens,
		repeatLimit: loopDetectRepeat,
		perToolErrs: map[string]int{},
		seen:        map[[20]byte]int{},
	}
}

func (g *turnGuard) preThink(iter int) (string, string, bool) {
	if iter > g.maxIter {
		return StopIterations, fmt.Sprintf("iteration cap (%d) reached", g.maxIter), true
	}
	if time.Now().After(g.deadline) {
		return StopTime, fmt.Sprintf("turn time budget (%s) exhausted", g.budget), true
	}
	if g.tokens > g.maxTokens {
		return StopTokens, fmt.Sprintf("turn token budget (%d) exhausted at %d", g.maxTokens, g.tokens), true
	}
	return "", "", false
}

func (g *turnGuard) addTokens(n int) { g.tokens += n }

func (g *turnGuard) toolError(name string) (string, bool) {
	g.totalErrs++
	g.perToolErrs[name]++
	if g.perToolErrs[name] >= 3 {
		return fmt.Sprintf("tool %q failed %d times", name, g.perToolErrs[name]), true
	}
	if g.totalErrs >= 5 {
		return fmt.Sprintf("%d total tool failures this turn", g.totalErrs), true
	}
	return "", false
}

func (g *turnGuard) toolOK() {}

// repeatedResult trips the loop detector only when the SAME call produced the
// SAME result repeatedly — i.e. the model is genuinely stuck, nothing changing.
// Re-running an identical command that yields DIFFERENT output (e.g. `npm run
// build` while iteratively fixing config, so the error moves file to file) is
// legitimate progress, not a loop, and must not be killed. Called AFTER exec,
// once the result is known.
func (g *turnGuard) repeatedResult(name string, args json.RawMessage, result string) (string, bool) {
	buf := append([]byte(name), args...)
	buf = append(buf, 0)
	buf = append(buf, []byte(result)...)
	sig := sha1.Sum(buf)
	g.seen[sig]++
	if g.seen[sig] >= g.repeatLimit {
		return fmt.Sprintf("same tool call with identical result repeated %d times (%s) — no progress", g.seen[sig], name), true
	}
	return "", false
}

// repeatingResult reports whether a call has just produced its SECOND
// identical result — one short of the kill threshold. The loop uses it to
// coach the model out of the loop instead of only killing it afterwards.
func (g *turnGuard) repeatingResult(name string, args json.RawMessage, result string) bool {
	buf := append([]byte(name), args...)
	buf = append(buf, 0)
	buf = append(buf, []byte(result)...)
	sig := sha1.Sum(buf)
	return g.seen[sig] >= 2 && g.seen[sig] < g.repeatLimit
}
