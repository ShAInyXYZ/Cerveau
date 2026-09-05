package loop

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
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
	StopStalled     = "guard_no_progress"
)

type turnGuard struct {
	// fp is the workspace fingerprint at the last observeWorkspace; seenAt is
	// the fingerprint under which each signature's count was accumulated.
	fp          uint64
	seenAt      map[[20]byte]uint64
	maxIter     int
	deadline    time.Time
	maxTokens   int
	repeatLimit int

	tokens      int
	extensions  int // token-budget checkpoints already granted this turn
	iterExts    int // iteration-cap extensions already granted this turn
	perToolErrs map[string]int
	totalErrs   int
	budget      time.Duration
	started     time.Time
	seen        map[[20]byte]int // count of (name+args+result) triples seen

	lastText    [20]byte                // the previous reply's normalised text
	textRepeats int                     // consecutive identical replies
	sameOut     map[[20]byte]int        // count of (tool, normalised result) regardless of args
	pure        map[[20]byte]pureResult // last result of a pure read, keyed by call
}

// pureResult remembers what a deterministic read of the workspace returned,
// and which workspace it read.
type pureResult struct {
	fp  uint64
	out string
}

// pureTools answer as a function of the workspace alone. Re-running one of
// them on an unchanged workspace is a wasted call with a known answer.
var pureTools = map[string]bool{"check_page": true, "read": true, "grep": true, "find_symbol": true}

func (g *turnGuard) rememberPureResult(name string, args json.RawMessage, fp uint64, out string) {
	if !pureTools[name] || fp == 0 {
		return
	}
	g.pure[sha1.Sum(append([]byte(name+"\x00"), args...))] = pureResult{fp: fp, out: out}
}

// cachedPureResult returns the previous result when the SAME call (exact
// args) was already made against the SAME workspace fingerprint.
func (g *turnGuard) cachedPureResult(name string, args json.RawMessage, fp uint64) (string, bool) {
	if !pureTools[name] || fp == 0 {
		return "", false
	}
	r, ok := g.pure[sha1.Sum(append([]byte(name+"\x00"), args...))]
	if !ok || r.fp != fp {
		return "", false
	}
	return r.out, true
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
		seenAt:      map[[20]byte]uint64{},
		perToolErrs: map[string]int{},
		seen:        map[[20]byte]int{},
		sameOut:     map[[20]byte]int{},
		pure:        map[[20]byte]pureResult{},
	}
}

func (g *turnGuard) preThink(iter int) (string, string, bool) {
	if iter > g.maxIter+g.iterExts*g.maxIter {
		return StopIterations, fmt.Sprintf("iteration cap (%d) reached", g.maxIter+g.iterExts*g.maxIter), true
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

// tokensExhausted reports whether the current budget slice is spent.
func (g *turnGuard) tokensExhausted() bool { return g.tokens > g.maxTokens }

// extendIter raises the iteration cap by one more slice, up to
// maxIterExtensions per turn. Like the token budget, iterations measure
// EFFORT, not stuckness — the repeat detector, error threshold and idle
// timeout catch genuine spinning, so a long productive build should not die
// at an arbitrary count. The extension bound is the runaway backstop.
func (g *turnGuard) extendIter() bool {
	if g.iterExts >= maxIterExtensions {
		return false
	}
	g.iterExts++
	return true
}

// extendTokens grants a fresh budget slice, up to maxTokenExtensions per turn.
// Exhaustion is a CHECKPOINT, not a failure: everything the turn did is in the
// episodic log, so continuing with a rebuilt (compressed) window is safe. The
// extension cap is the runaway backstop.
func (g *turnGuard) extendTokens() bool {
	if g.extensions >= maxTokenExtensions {
		return false
	}
	g.extensions++
	g.tokens = 0
	return true
}

// toolError decides whether a failure ends the turn. Three failures of the
// same TOOL used to be enough — which killed a chess build in the middle of
// debugging its own test script, where every failure was a different error
// and each one was progress (2026-09-04). A test loop fails many times on
// the way to passing; what ends a turn is the SAME failure coming back after
// the model has been told about it. So: the same tool failing with the same
// error line four times (the breaker coaches at three), or an implausible
// number of distinct failures, is a stop. Different errors are debugging.
func (g *turnGuard) toolError(name, out string) (string, bool) {
	g.totalErrs++
	g.perToolErrs[name]++
	wall := name + "\x00" + errorLine(out)
	g.perToolErrs[wall]++
	if g.perToolErrs[wall] >= sameWallLimit {
		return fmt.Sprintf("tool %q failed %d times with the same error: %s", name, g.perToolErrs[wall], errorLine(out)), true
	}
	if g.perToolErrs[name] >= distinctFailLimit {
		return fmt.Sprintf("tool %q failed %d times", name, g.perToolErrs[name]), true
	}
	if g.totalErrs >= totalFailLimit {
		return fmt.Sprintf("%d total tool failures this turn", g.totalErrs), true
	}
	return "", false
}

const (
	sameWallLimit     = 4  // identical error line, same tool — coached at 3, stopped at 4
	distinctFailLimit = 10 // one tool, different errors, with NO success in between
	totalFailLimit    = 30 // all tools, whole turn — the runaway backstop, not a judgement
)

// toolOK: a success closes the streak. Distinct failures are only a signal
// while nothing succeeds in between — 8 bash failures interleaved with 9
// successes and 4 edits is a probe-fix-test cycle, and the cap ended it on
// the call right after the model found its bug (2026-09-04). A successful
// write or edit means a new attempt: every tool's distinct count restarts.
// Same-wall counts (name+error line) are NOT reset here: fixing something
// else and hitting the identical error again is still the same wall.
func (g *turnGuard) toolOK(name string) {
	delete(g.perToolErrs, name)
	if name == "write" || name == "edit" {
		for k := range g.perToolErrs {
			if !strings.Contains(k, "\x00") {
				delete(g.perToolErrs, k)
			}
		}
	}
}

// digits is what varies between "the same" call: temp-file suffixes, ports,
// timestamps, byte counts. The four bash calls of the 2026-09-04 loop differed
// ONLY in `.crv-eval-3314114930.html` vs `.crv-eval-2207781145.html`, so an
// exact hash saw four different calls and never tripped.
var digits = regexp.MustCompile(`(\.crv-eval-)\d+(\.html)`)

func normalize(s string) string {
	return digits.ReplaceAllString(strings.Join(strings.Fields(s), " "), "${1}#${2}")
}

// observeWorkspace tells the guard what the workspace looks like now. A result
// identical to an earlier one is only evidence of a LOOP if nothing changed in
// between. Re-checking a page after an edit and getting the same answer is the
// model learning that the edit did not help — normal work, and it was being
// coached as a repeat one call after a write (2026-09-05, NFQ). The genuine loop
// on that same run — six identical probes on an untouched file — still trips,
// because the fingerprint never moved.
func (g *turnGuard) observeWorkspace(fp uint64) { g.fp = fp }

// freshen resets a signature's count when the workspace has moved since it was
// last counted, so the count only ever spans an unchanged tree.
func (g *turnGuard) freshen(sig [20]byte) {
	if at, ok := g.seenAt[sig]; !ok || at != g.fp {
		g.seen[sig] = 0
		g.seenAt[sig] = g.fp
	}
}

func (g *turnGuard) sig(name string, args json.RawMessage, result string) [20]byte {
	buf := append([]byte(name), []byte(normalize(string(args)))...)
	buf = append(buf, 0)
	buf = append(buf, []byte(normalize(result))...)
	return sha1.Sum(buf)
}

// seenBefore reports whether this (call, result) pair has already happened,
// without counting it. The loop uses it to decide whether a tool result is
// PROGRESS: only a successful call with an output the turn has not seen
// restarts the idle clock. A failure, or the same output again, is standing
// still — and standing still is what the idle guard is for.
func (g *turnGuard) seenBefore(name string, args json.RawMessage, result string) bool {
	sig := g.sig(name, args, result)
	g.freshen(sig)
	return g.seen[sig] > 0
}

// sawText watches the model's own words. A reply repeated verbatim is the
// clearest loop signal there is — four times "The IIFE return is the problem"
// in one turn — and it is invisible to every tool-based detector, because the
// tool calls around it can differ in details that do not matter. Two in a row:
// a hint. Three: the turn stops.
func (g *turnGuard) sawText(content string) (hint string, stop bool) {
	n := normalize(content)
	if len(n) < 20 { // "Done." and empty preambles are not a loop
		g.textRepeats = 0
		return "", false
	}
	h := sha1.Sum([]byte(n))
	if h == g.lastText {
		g.textRepeats++
	} else {
		g.lastText = h
		g.textRepeats = 1
	}
	switch {
	case g.textRepeats >= g.repeatLimit:
		return fmt.Sprintf("the model repeated the same message %d times in a row — no progress", g.textRepeats), true
	case g.textRepeats == 2:
		return "You have now said exactly this twice, and the same actions followed both times. " +
			"Repeating it will produce the same failure. Either take a genuinely different action, " +
			"read the error again and address what it actually says, or stop and report what is blocking you.", false
	}
	return "", false
}

// sameResultAgain watches the RESULT alone. The 2026-09-04 chess run rewrote
// its check_page eval script six times — a different script each time, so the
// (call, result) detector never matched — and got "no legal move d1-h5" back
// from every one of them. When the input keeps changing and the output does
// not, the input is not the problem; something the model is not looking at
// is. Four: a pointed hint. Six: the turn stops.
func (g *turnGuard) sameResultAgain(name, result string, ok bool) (hint string, stop bool) {
	n := normalize(result)
	if len(n) < 20 {
		return "", false // "ok", "", "[]" — not a signal
	}
	if ok && !errorish.MatchString(result) {
		return "", false // a plain success repeated is fine: edits, writes, reads that agree
	}
	h := sha1.Sum([]byte(name + "\x00" + n))
	g.sameOut[h]++
	switch c := g.sameOut[h]; {
	case c >= sameResultStop:
		return fmt.Sprintf("%s returned the same result %d times for different inputs — no progress", name, c), true
	case c == sameResultHint:
		return fmt.Sprintf("You have now called %s with %d DIFFERENT inputs and received the SAME result every time. "+
			"The input is not what is wrong. Stop rewriting it. Read the code that produces this result, "+
			"or check the assumption behind the test itself — then change THAT.", name, c), false
	}
	return "", false
}

const (
	sameResultHint = 4
	sameResultStop = 6
)

// repeatedResult trips the loop detector only when the SAME call produced the
// SAME result repeatedly — i.e. the model is genuinely stuck, nothing changing.
// Re-running an identical command that yields DIFFERENT output (e.g. `npm run
// build` while iteratively fixing config, so the error moves file to file) is
// legitimate progress, not a loop, and must not be killed. Called AFTER exec,
// once the result is known.
func (g *turnGuard) repeatedResult(name string, args json.RawMessage, result string) (string, bool) {
	sig := g.sig(name, args, result)
	g.freshen(sig)
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
	sig := g.sig(name, args, result)
	g.freshen(sig)
	return g.seen[sig] >= 2 && g.seen[sig] < g.repeatLimit
}
