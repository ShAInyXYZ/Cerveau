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
	started     time.Time
	seen        map[[20]byte]int // count of (name+args+result) triples seen
}

func newTurnGuard(maxIter int) *turnGuard { return newTurnGuardBudget(maxIter, maxTurnTime) }

// newTurnGuardBudget sets the IDLE budget: how long a turn may go without
// making progress. It is deliberately not a total-duration cap — a slow
// local model doing real work would be killed exactly like one spinning in
// a loop. Every tool result, every successful call resets the clock (see
// progress()), so only a genuinely stuck turn trips it.
func newTurnGuardBudget(maxIter int, budget time.Duration) *turnGuard {
	if maxIter <= 0 {
		maxIter = maxIterations
	}
	if budget <= 0 {
		budget = maxTurnTime
	}
	return &turnGuard{
		budget:      budget,
		started:     time.Now(),
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
		return StopTime, fmt.Sprintf("no progress for %s — turn stalled (total elapsed %s)",
			g.budget, time.Since(g.started).Round(time.Second)), true
	}
	if g.tokens > g.maxTokens {
		return StopTokens, fmt.Sprintf("turn token budget (%d) exhausted at %d", g.maxTokens, g.tokens), true
	}
	return "", "", false
}

// progress restarts the idle clock: the turn just did something real (a
// tool returned, a file changed). Duration is not the failure signal —
// standing still is.
func (g *turnGuard) progress() { g.deadline = time.Now().Add(g.budget) }

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
