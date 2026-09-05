package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
)

func setup(t *testing.T, respond func(call int) map[string]any) (*Loop, string, *int) {
	t.Helper()
	tmp := t.TempDir()
	calls := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(respond(*calls))
	}))
	t.Cleanup(srv.Close)

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)

	os.WriteFile(filepath.Join(tmp, "f.txt"), []byte("data"), 0o644)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	return l, eventsPath, calls
}

func toolCallReply(id string) map[string]any {
	return map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"id":   id,
					"type": "function",
					"function": map[string]string{
						"name":      "read",
						"arguments": `{"path":"f.txt"}`,
					},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
	}
}

// A tool whose output changes every call — models legitimately re-run the same
// command (e.g. `npm run build`) while fixing things, and each run reports a
// different error. That is progress, NOT a stuck loop, and must reach the
// iteration cap rather than being killed by loop detection.
type changingTool struct{ n int }

func (c *changingTool) Name() string           { return "runner" }
func (c *changingTool) Description() string    { return "returns changing output" }
func (c *changingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (c *changingTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	c.n++
	return "error variant " + string(rune('A'+c.n)), nil
}

func TestSameCallDifferentResultIsNotALoop(t *testing.T) {
	tmp := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "c", "type": "function",
					"function": map[string]string{"name": "runner", "arguments": `{}`},
				}}},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)
	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	os.WriteFile(eventsPath, nil, 0o644)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: &changingTool{}, RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	res, err := l.Run(context.Background(), "s1", "keep going", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	// must NOT be killed by the loop detector — same command, changing output
	if res.StopReason == StopLoop {
		t.Fatalf("loop detector tripped on changing output — should have allowed progress")
	}
	if res.StopReason != StopIterations {
		t.Fatalf("stop = %s, want %s (ran to iteration cap)", res.StopReason, StopIterations)
	}
}

func TestLoopDetectionTrips(t *testing.T) {
	n := 0
	l, eventsPath, _ := setup(t, func(call int) map[string]any {
		n++
		return toolCallReply("call_1")
	})
	res, err := l.Run(context.Background(), "s1", "loop please", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != StopLoop {
		t.Fatalf("stop = %s, want %s", res.StopReason, StopLoop)
	}
	if !res.Capped {
		t.Fatal("expected capped result")
	}
	events, _ := episodic.Replay(eventsPath)
	var guardErr *episodic.Event
	for i, ev := range events {
		if ev.Type == episodic.Err {
			guardErr = &events[i]
		}
	}
	if guardErr == nil {
		t.Fatal("no guard error event recorded")
	}
	var p struct {
		Class string `json:"class"`
		Stop  string `json:"stop"`
	}
	json.Unmarshal(guardErr.Payload, &p)
	if p.Class != "guard" || p.Stop != StopLoop {
		t.Fatalf("guard event = %+v", p)
	}
}

func TestTokenBudgetTrips(t *testing.T) {
	l, _, _ := setup(t, func(call int) map[string]any {
		r := toolCallReply("call_1")
		r["usage"] = map[string]int{"prompt_tokens": 10, "completion_tokens": 20000}
		return r
	})
	res, err := l.Run(context.Background(), "s1", "burn tokens", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != StopTokens && res.StopReason != StopLoop {
		t.Fatalf("stop = %s, want guard_tokens (or loop guard first)", res.StopReason)
	}
}

func TestFinalAnswerStopReason(t *testing.T) {
	l, _, _ := setup(t, func(call int) map[string]any {
		return map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "done"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 3},
		}
	})
	res, err := l.Run(context.Background(), "s1", "hi", "discussion")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != StopFinalAnswer || res.Capped {
		t.Fatalf("res = %+v", res)
	}
}

// Exhausting the token budget should be a CHECKPOINT, not a death: the guard
// grants a bounded number of budget extensions (the work is persisted in the
// episodic log, so continuing is safe). Only when the extensions run out does
// it trip for good — that's the runaway backstop.
func TestTokenBudgetExtends(t *testing.T) {
	g := newTurnGuard(100)
	g.addTokens(maxTurnTokens + 1)
	if !g.tokensExhausted() {
		t.Fatal("budget should read exhausted")
	}
	// three extensions granted...
	for i := 0; i < maxTokenExtensions; i++ {
		if !g.extendTokens() {
			t.Fatalf("extension %d should be granted", i+1)
		}
		if g.tokensExhausted() {
			t.Fatal("after an extension the budget must be fresh")
		}
		g.addTokens(maxTurnTokens + 1)
	}
	// ...then it's over
	if g.extendTokens() {
		t.Fatal("extensions beyond the cap must be refused")
	}
	if _, _, tripped := g.preThink(1); !tripped {
		t.Fatal("exhausted with no extensions left must trip preThink")
	}
}

// The iteration cap must extend for a PROGRESSING turn, exactly like the token
// budget: a 20-minute build legitimately spends 40+ iterations; the repeat
// detector and idle timeout catch true spinning. Bounded extensions keep the
// runaway backstop.
func TestIterationCapExtends(t *testing.T) {
	g := newTurnGuard(40)
	if _, _, tripped := g.preThink(40); tripped {
		t.Fatal("at the cap should still run")
	}
	if _, _, tripped := g.preThink(41); !tripped {
		t.Fatal("past the cap should trip without an extension")
	}
	if !g.extendIter() {
		t.Fatal("first extension should be granted")
	}
	if _, _, tripped := g.preThink(41); tripped {
		t.Fatal("extension should raise the cap")
	}
	for i := 0; i < maxIterExtensions-1; i++ {
		if !g.extendIter() {
			t.Fatalf("extension %d should be granted", i+2)
		}
	}
	if g.extendIter() {
		t.Fatal("extensions beyond the cap must be refused")
	}
}

// Temp-file suffixes, ports and counters are what "the same" call varies by.
// Four bash calls that differed only in .crv-eval-<n>.html must count as one.
func TestRepeatDetectionIgnoresNumbers(t *testing.T) {
	g := newTurnGuard(100)
	for i, n := range []string{"3314114930", "2207781145", "998"} {
		args := json.RawMessage(`{"command":"node fix.js .crv-eval-` + n + `.html"}`)
		_, tripped := g.repeatedResult("bash", args, "temp file gone\nexit: exit status 2")
		if i < 2 && tripped {
			t.Fatalf("tripped early at %d", i)
		}
		if i == 2 && !tripped {
			t.Fatal("third near-identical failure should trip the loop detector")
		}
	}
}

// The model saying the identical sentence three times is a loop whatever
// its tool calls look like. Two in a row earns a hint, three stops the turn.
func TestRepeatedAssistantTextIsALoop(t *testing.T) {
	g := newTurnGuard(100)
	msg := "The IIFE return is the problem. Let me use a comma expression that evaluates to the final value:"
	if hint, stop := g.sawText(msg); hint != "" || stop {
		t.Fatal("first occurrence is not a repeat")
	}
	if hint, stop := g.sawText(msg); hint == "" || stop {
		t.Fatal("second occurrence should coach, not stop")
	}
	if _, stop := g.sawText(msg); !stop {
		t.Fatal("third occurrence should stop the turn")
	}
	g2 := newTurnGuard(100)
	for i := 0; i < 5; i++ {
		if _, stop := g2.sawText("Done."); stop {
			t.Fatal("short acknowledgements are never a loop")
		}
	}
}

// Only a successful, new result is progress. A failure or a repeat must not
// restart the idle clock, or a model calling failing tools never stalls.
func TestOnlyFreshSuccessIsProgress(t *testing.T) {
	g := newTurnGuard(100)
	args := json.RawMessage(`{"command":"ls"}`)
	if g.seenBefore("bash", args, "a b c") {
		t.Fatal("first result cannot have been seen")
	}
	g.repeatedResult("bash", args, "a b c")
	if !g.seenBefore("bash", args, "a b c") {
		t.Fatal("same result again is not new")
	}
	if !g.seenBefore("bash", args, "a  b   c") {
		t.Fatal("whitespace differences in a result are not a new result")
	}
	if g.seenBefore("bash", args, "a b c d") {
		t.Fatal("a genuinely different result is new")
	}
}

// Debugging is failing differently each time. Three DIFFERENT bash errors
// must not end a turn; the same error four times must.
func TestDifferentFailuresAreDebuggingNotALoop(t *testing.T) {
	g := newTurnGuard(100)
	for i, out := range []string{
		"ReferenceError: Chess is not defined\nexit: exit status 1",
		"Error: illegal move g1-f3 status=ongoing\nexit: exit status 1",
		"TypeError: w.state is undefined\nexit: exit status 1",
	} {
		if detail, tripped := g.toolError("bash", out); tripped {
			t.Fatalf("distinct failure %d ended the turn: %s", i, detail)
		}
	}
	g2 := newTurnGuard(100)
	same := "Error: illegal move g1-f3 status=ongoing\nexit: exit status 1"
	for i := 0; i < 3; i++ {
		if _, tripped := g2.toolError("bash", same); tripped {
			t.Fatalf("same error should be coached before being stopped (at %d)", i)
		}
	}
	if detail, tripped := g2.toolError("bash", same); !tripped || !strings.Contains(detail, "same error") {
		t.Fatalf("fourth identical failure should stop: %v %q", tripped, detail)
	}
}

// Six different eval scripts, one identical error: the script is not the
// problem. Hint at four, stop at six. Distinct results are never counted.
func TestSameResultForDifferentInputs(t *testing.T) {
	g := newTurnGuard(100)
	out := `eval result: EVAL ERROR: no legal move d1-h5", source: index.html (184)`
	var hint string
	var stop bool
	for i := 1; i <= sameResultStop; i++ {
		hint, stop = g.sameResultAgain("check_page", out, true)
		switch {
		case i < sameResultHint && (hint != "" || stop):
			t.Fatalf("too early at %d", i)
		case i == sameResultHint && (hint == "" || stop):
			t.Fatalf("expected a hint at %d", i)
		case i == sameResultStop && !stop:
			t.Fatalf("expected a stop at %d", i)
		}
	}
	g2 := newTurnGuard(100)
	for i := 0; i < 10; i++ {
		if _, stop := g2.sameResultAgain("check_page", fmt.Sprintf("eval result: EVAL ERROR: case %c failed differently", 'a'+i), true); stop {
			t.Fatal("different results must never stop the turn")
		}
	}
}

// A second identical check_page on an unchanged workspace is answered from
// memory; any write invalidates it; bash is never cached.
func TestPureReadsAreCachedPerWorkspace(t *testing.T) {
	g := newTurnGuard(100)
	args := json.RawMessage(`{"path":"index.html","eval":"1+1"}`)
	if _, ok := g.cachedPureResult("check_page", args, 42); ok {
		t.Fatal("nothing cached yet")
	}
	g.rememberPureResult("check_page", args, 42, "eval result: 2")
	if out, ok := g.cachedPureResult("check_page", args, 42); !ok || out != "eval result: 2" {
		t.Fatalf("expected cached result, got %q %v", out, ok)
	}
	if _, ok := g.cachedPureResult("check_page", args, 43); ok {
		t.Fatal("a changed workspace must not be served from cache")
	}
	g.rememberPureResult("bash", json.RawMessage(`{"command":"date"}`), 42, "now")
	if _, ok := g.cachedPureResult("bash", json.RawMessage(`{"command":"date"}`), 42); ok {
		t.Fatal("bash is never cached")
	}
	if _, ok := g.cachedPureResult("check_page", args, 0); ok {
		t.Fatal("no workspace fingerprint means no caching")
	}
}

// Failures between successes are a debugging cycle, not a wall. The count
// restarts on a success of the same tool and on any successful edit; the
// same-wall count survives both.
func TestSuccessResetsDistinctFailures(t *testing.T) {
	g := newTurnGuard(100)
	for i := 0; i < 20; i++ {
		if _, tripped := g.toolError("bash", fmt.Sprintf("Error: probe %c", 'a'+i)); tripped {
			t.Fatalf("tripped at %d despite successes in between", i)
		}
		g.toolOK("bash")
	}
	g2 := newTurnGuard(100)
	for i := 0; i < 3; i++ {
		g2.toolError("bash", "Error: same wall")
		g2.toolOK("edit")
	}
	if _, tripped := g2.toolError("bash", "Error: same wall"); !tripped {
		t.Fatal("the same wall four times is a stop even with edits in between")
	}
}

// Thinking follows the mode: autopilot turns think, chat turns do not, and
// "always" thinks everywhere. Unknown input falls back to off / medium.
func TestThinkingFollowsMode(t *testing.T) {
	l := &Loop{}
	if got := l.thinkingFor("autopilot"); got != "off" {
		t.Fatalf("default execution steps do not think, got %q", got)
	}
	if got := l.planThinkingFor("autopilot"); got != "low" {
		t.Fatalf("default planning call thinks at low, got %q", got)
	}
	if got := l.thinkingFor("discussion"); got != "off" {
		t.Fatalf("chat should not think by default, got %q", got)
	}
	l.SetThinking("autopilot", "xhigh")
	if l.thinkingFor("autopilot") != "xhigh" || l.thinkingFor("discussion") != "off" {
		t.Fatal("autopilot mode should think only in autopilot")
	}
	l.SetThinking("always", "low")
	if l.thinkingFor("discussion") != "low" {
		t.Fatal("always should think in chat too")
	}
	l.SetThinking("bogus", "bogus")
	if m, e := l.Thinking(); m != "plan" || e != "low" {
		t.Fatalf("bad input should fall back, got %s %s", m, e)
	}
}

// The exact text Qwen3.8 produced twice for the planning call, translated
// into a committed plan: names, files and descriptions survive.
func TestPlanFromXMLStyleText(t *testing.T) {
	dir := t.TempDir()
	wr, err := episodic.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := "\n\n<commit_plan>\n<steps>\n  <step name=\"skeleton\" files=\"Cantilever.html\" description=\"HTML skeleton, Three.js CDN import, canvas\">first</step>\n" +
		"  <step name=\"physics\" files=\"Cantilever.html\">Deck rotation on two axes, torque from placed masses</step>\n" +
		"  <step name=\"verify\" files=\"Cantilever.html, test.js\" description=\"check_page run\"></step>\n</steps>\n</commit_plan>"
	p, note := planFromText(wr, text)
	if p == nil || len(p.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %+v", p)
	}
	if p.Steps[0].Title != "skeleton" || p.Steps[0].Detail != "HTML skeleton, Three.js CDN import, canvas" || p.Steps[0].Files[0] != "Cantilever.html" {
		t.Fatalf("step 1 wrong: %+v", p.Steps[0])
	}
	if p.Steps[1].Detail != "Deck rotation on two axes, torque from placed masses" {
		t.Fatalf("body should become the detail when no description attr: %+v", p.Steps[1])
	}
	if len(p.Steps[2].Files) != 2 || !strings.Contains(note, "translated") {
		t.Fatalf("files list / note wrong: %+v %q", p.Steps[2], note)
	}
	if p2, _ := planFromText(wr, "Now the chess engine:"); p2 != nil {
		t.Fatal("ordinary prose is not a plan")
	}
}

// Six successful edits all say "edited cantilever.html": that is progress,
// not a loop. Only error-bearing results are counted.
func TestSameResultIgnoresPlainSuccesses(t *testing.T) {
	g := newTurnGuard(100)
	for i := 0; i < 10; i++ {
		if _, stop := g.sameResultAgain("edit", "edited cantilever.html: replaced 1 occurrence, 12 lines", true); stop {
			t.Fatal("repeated successful edits must never stop a turn")
		}
	}
	for i := 0; i < sameResultStop-1; i++ {
		g.sameResultAgain("bash", "Error: illegal move g1-f3\nexit: exit status 1", false)
	}
	if _, stop := g.sameResultAgain("bash", "Error: illegal move g1-f3\nexit: exit status 1", false); !stop {
		t.Fatal("the same failure for different inputs still stops")
	}
}

// Crane6 wrote self-closing steps with description attributes; Crane5 wrote
// open/close pairs. Both are plans.
func TestPlanFromSelfClosingSteps(t *testing.T) {
	dir := t.TempDir()
	wr, _ := episodic.Open(filepath.Join(dir, "events.jsonl"))
	text := "\n\n<commit_plan>\n<steps>\n  <step name=\"HTML shell + Three.js scene setup\" files=\"cantilever.html\" description=\"Write the HTML skeleton, import Three.js from CDN. ~180 lines.\"/>\n" +
		"  <step name=\"Physics core: deck rotation + torque\" files=\"cantilever.html\" description=\"Append the physics module (r x m*g). ~180 lines.\"/>\n" +
		"  <step name=\"Verify\" files=\"cantilever.html\" description=\"check_page: canvas > 0, tilt changes\"/>\n</steps>\n</commit_plan>"
	p, _ := planFromText(wr, text)
	if p == nil || len(p.Steps) != 3 {
		t.Fatalf("expected 3 self-closing steps, got %+v", p)
	}
	if p.Steps[0].Title != "HTML shell + Three.js scene setup" || !strings.Contains(p.Steps[1].Detail, "r x m*g") || !strings.Contains(p.Steps[2].Detail, "canvas > 0") {
		t.Fatalf("attributes lost: %+v", p.Steps)
	}
}

// A result identical to an earlier one is only a repeat if the workspace did
// not change in between. Re-checking after an edit and getting the same answer
// is the model learning the edit did not help; six identical probes on an
// untouched file is a loop. The guard must tell them apart.
func TestRepeatGuardResetsWhenTheWorkspaceMoves(t *testing.T) {
	g := newTurnGuard(0)
	args := json.RawMessage(`{"eval":"probe"}`)

	// edit, check, edit, check, edit, check — same answer every time
	for fp := uint64(1); fp <= 3; fp++ {
		g.observeWorkspace(fp)
		if _, tripped := g.repeatedResult("check_page", args, "same"); tripped {
			t.Fatalf("a re-check after an edit must never trip the loop guard (fp %d)", fp)
		}
		if g.repeatingResult("check_page", args, "same") {
			t.Fatalf("a re-check after an edit must not be coached as a repeat (fp %d)", fp)
		}
	}

	// now nothing changes: the same probe three times IS a loop
	g.observeWorkspace(3)
	var tripped bool
	for i := 0; i < 3; i++ {
		_, tripped = g.repeatedResult("check_page", args, "same")
	}
	if !tripped {
		t.Fatal("three identical probes on an unchanged workspace must trip the guard")
	}
}
