package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/memory"
	"cerveau/internal/rfx"
	"cerveau/internal/skills"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

// envBlock gives the model concrete ground truth about where it is running, so
// it never has to guess its workspace path (it used to hallucinate "workspace
// root" / "appears empty"). Read live — the workspace can change at runtime.
func (l *Loop) envBlock(sessionID string) string {
	if l.workspace == nil {
		return ""
	}
	ws := l.workspace(sessionID)
	if ws == "" {
		return ""
	}
	state := "exists"
	if entries, err := os.ReadDir(ws); err != nil {
		state = "not accessible"
	} else if len(entries) == 0 {
		state = "empty"
	} else {
		state = fmt.Sprintf("%d top-level entries", len(entries))
	}
	env := fmt.Sprintf("\n\nEnvironment: your workspace is %s (%s). All file tools are rooted here. When asked where you are or what path you're on, answer with this exact path — do not guess.", ws, state)
	// The harness's own stack occupies ports on this machine. Without this the
	// model suggests serving things it builds on ports already in use (it told
	// the user `python3 -m http.server 8080` — llama-server's own port).
	if l.stackInfo != nil {
		if s := l.stackInfo(); s != "" {
			env += "\n" + s
		}
	}
	return env
}

// SetStackFunc wires a live description of the harness's own running services
// (ports in use) so the model never suggests colliding with them.
func (l *Loop) SetStackFunc(f func() string) { l.stackInfo = f }

const (
	maxIterations = 8
	maxTurnTime   = 4 * time.Minute
	// a supervised plan step builds files; it needs a build-sized budget
	maxStepTime      = 20 * time.Minute
	maxTurnTokens    = 16384
	loopDetectRepeat = 3
)

type Loop struct {
	llm          *llm.Client
	toolsMu      sync.RWMutex
	tools        *tools.Registry
	open         func(sessionID string) (*episodic.Writer, error)
	path         func(sessionID string) string
	win          *window.Manager
	recall       *memory.Recall
	runs         *runsRegistry
	curator      *memory.Curator
	skills       *skills.Loader
	skillFactory func([]skills.SkillTool) []tools.Tool
	rfx          *rfx.Loader
	workspace    func(sessionID string) string // the SESSION's workspace path (per-session, e.g. instant scratch)
	stackInfo    func() string                 // the harness's own running services + reserved ports
	isInstant    func(id string) bool          // is this session an ephemeral instant session?
	bg           sync.WaitGroup
}

// SetWorkspaceFunc wires a live getter for the active workspace path so the
// system prompt can tell the model where it actually is.
func (l *Loop) SetWorkspaceFunc(f func(sessionID string) string) { l.workspace = f }

func (l *Loop) SetRegistry(r *tools.Registry) {
	l.toolsMu.Lock()
	l.tools = r
	l.toolsMu.Unlock()
}

func (l *Loop) registry() *tools.Registry {
	l.toolsMu.RLock()
	defer l.toolsMu.RUnlock()
	return l.tools
}

// runBoundary launches the async turn-boundary hooks under the background
// WaitGroup so shutdown can drain in-flight distill/promotion work.
func (l *Loop) runBoundary(sessionID string) {
	l.bg.Add(1)
	go func() {
		defer l.bg.Done()
		l.boundaryHooks(sessionID)
	}()
}

// WaitBackground blocks until in-flight boundary goroutines finish or the
// timeout elapses. Called on shutdown so a just-learned fact isn't lost.
func (l *Loop) WaitBackground(timeout time.Duration) {
	done := make(chan struct{})
	go func() { l.bg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func New(llmClient *llm.Client, reg *tools.Registry, openWriter func(string) (*episodic.Writer, error), eventsPath func(string) string, win *window.Manager) *Loop {
	return &Loop{llm: llmClient, tools: reg, open: openWriter, path: eventsPath, win: win, runs: newRunsRegistry()}
}

func (l *Loop) SetRecall(r *memory.Recall) { l.recall = r }

func (l *Loop) SetSkills(s *skills.Loader, f func([]skills.SkillTool) []tools.Tool) {
	l.skills = s
	l.skillFactory = f
}

// SetReflexes wires the RFX loader. Reflexes are applied per turn in Run —
// a file dropped into ~/.crv/rfx goes live on the next turn, no restart.
func (l *Loop) SetReflexes(r *rfx.Loader) { l.rfx = r }

// RunReflex executes one reflex manually (RFX_UI dock quick-run) through the
// SAME path the model uses: registry copy with reflexes → guard → remediator
// → dispatch. Mode "" means mode-fencing doesn't restrict it — the human
// clicked the button, which is its own authorization.
func (l *Loop) RunReflex(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if l.rfx == nil {
		return "", fmt.Errorf("rfx not wired")
	}
	reg, errs := l.registry().WithReflexes(l.rfx.List())
	if len(errs) > 0 {
		return "", fmt.Errorf("reflex registration: %v", errs[0])
	}
	if _, ok := reg.Entry(name); !ok {
		return "", fmt.Errorf("no enabled reflex named %q", name)
	}
	return reg.ExecuteMode(ctx, name, args, "")
}

type Result struct {
	Reply      string         `json:"reply"`
	Iterations int            `json:"iterations"`
	Capped     bool           `json:"capped"`
	StopReason string         `json:"stop_reason"`
	Pulls      int            `json:"pulls"`
	Window     *window.Report `json:"window,omitempty"`
}

func (l *Loop) Run(ctx context.Context, sessionID, userMsg, modeName string) (*Result, error) {
	mode := ModeByName(modeName)
	systemPrompt := basePrompt + l.envBlock(sessionID) + "\n\n" + mode.Module
	// In autopilot, a plan committed earlier (in Discussion) is injected as GUIDANCE
	// — the agent follows its intent but adapts freely. No plan is fine: it plans
	// and executes from the task directly.
	if mode.Name == "autopilot" {
		if plan, _, perr := LatestPlan(l.path(sessionID)); perr == nil && plan != nil {
			systemPrompt += "\n\n" + plan.AsGuidance()
		}
	}
	wr, err := l.open(sessionID)
	if err != nil {
		return nil, err
	}
	if _, err := wr.Append(episodic.MsgUser, map[string]string{"text": userMsg}); err != nil {
		return nil, err
	}
	g := newTurnGuardBudget(mode.MaxIter, turnBudget(ctx))
	var winRep window.Report
	var turnPulls, pendingPulls []memory.Pull
	if l.recall != nil {
		turnPulls = l.recall.TurnStart(ctx, sessionID, userMsg, l.tailEvtIDs(sessionID, 20))
	}
	sessionReg := l.registry()
	if l.rfx != nil {
		defs := l.rfx.List()
		reg, rfxErrs := sessionReg.WithReflexes(defs)
		sessionReg = reg
		for _, e := range rfxErrs {
			// Loud, never silent: a reflex that couldn't register is told
			// to the session log where the user and the report can see it.
			wr.Append(episodic.Note, map[string]string{"kind": "rfx_rejected", "text": e.Error()})
		}
		// Prompt link: the model must KNOW reflexes exist (a tool it doesn't
		// know about is a tool that doesn't exist) — and EVERY mode sees the
		// full inventory with mode tags, so "what reflexes do you have?" is
		// answerable anywhere, even where they're all fenced out.
		if len(defs) > 0 {
			avail := sessionReg.ReflexNames(mode.Name)
			var sb strings.Builder
			fmt.Fprintf(&sb, "\n\nRFX: %d pre-wired reflex tools are installed (from ~/.crv/rfx — typed, guard-checked; prefer a fitting reflex over raw bash). Installed: ", len(defs))
			for i, d := range defs {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(d.Name)
				if len(d.Modes) > 0 {
					sb.WriteString(" [" + strings.Join(d.Modes, ", ") + "]")
				} else {
					sb.WriteString(" [all modes]")
				}
			}
			sb.WriteString(".")
			if len(avail) > 0 {
				sb.WriteString(" Callable in THIS mode: " + strings.Join(avail, ", ") + ".")
			} else {
				sb.WriteString(" None are callable in this mode (mode-fenced); they activate in their declared modes.")
			}
			systemPrompt += sb.String()
			wr.Append(episodic.Note, map[string]string{"kind": "rfx_loaded", "text": fmt.Sprintf("%d reflexes loaded, %d available in %s", len(defs), len(avail), mode.Name)})
		}
	}
	var skillNotes []string
	if l.skills != nil {
		var st []tools.Tool
		for _, sk := range l.skills.Match(userMsg) {
			skillNotes = append(skillNotes, "## Loaded skill: "+sk.Name+"\n"+sk.CappedBody())
			if l.skillFactory != nil {
				st = append(st, l.skillFactory(sk.Tools)...)
			}
			wr.Append(episodic.Note, map[string]string{"kind": "skill_loaded", "text": "skill loaded: " + sk.Name})
		}
		if len(st) > 0 {
			sessionReg = l.tools.WithSessionTools(st)
		}
	}
	runCtx, rootCancel := context.WithCancel(ctx)
	h := &runHandle{rootCancel: rootCancel}
	defer l.runs.register(sessionID, h)()
	defer rootCancel()
	stop := func(res *Result, reason, detail string) *Result {
		wr.Append(episodic.Err, map[string]string{"class": "guard", "detail": detail, "stop": reason})
		res.Capped = true
		res.StopReason = reason
		res.Reply = detail + " — handing back"
		res.Window = &winRep
		return res
	}
	var correction string // one-shot corrective message injected after a truncated tool call
	truncated := 0        // how many times we've fed that correction back this turn
	for i := 1; ; i++ {
		if h.killed.Load() {
			wr.Append(episodic.Aborted, map[string]string{"phase": "turn", "reason": "killed by user"})
			return stop(&Result{Iterations: i - 1}, "killed", "killed by user — state preserved in episodic"), nil
		}
		if h.paused.Load() {
			return &Result{Iterations: i - 1, StopReason: "paused", Reply: "paused — resume anytime, the log is the state", Window: &winRep}, nil
		}
		if reason, detail, tripped := g.preThink(i); tripped {
			return stop(&Result{Iterations: i - 1}, reason, detail), nil
		}
		iterCtx, iterCancel := context.WithCancel(runCtx)
		h.setInFlight(iterCancel)
		messages, rep, err := l.buildMessages(iterCtx, sessionID, systemPrompt, append(turnPulls, pendingPulls...), skillNotes)
		pendingPulls = nil
		if err != nil {
			iterCancel()
			return nil, err
		}
		if correction != "" { // one-shot feedback after a truncated tool call
			messages = append(messages, llm.Message{Role: "user", Content: correction})
			correction = ""
		}
		winRep = rep
		reply, usage, err := l.completeWithRetry(iterCtx, wr, messages, sessionReg.Specs(mode.Name), "", mode.ProseCap)
		g.addTokens(usage.CompletionTokens)
		if err != nil {
			canceled := iterCtx.Err() == context.Canceled
			iterCancel()
			if h.killed.Load() {
				wr.Append(episodic.Aborted, map[string]string{"phase": "think", "reason": "killed by user"})
				return stop(&Result{Iterations: i - 1}, "killed", "killed by user — state preserved in episodic"), nil
			}
			// Only treat a cancellation as a steer if the user ACTUALLY steered.
			// An incidental cancel (flaky model endpoint, dropped connection) also
			// surfaces as context.Canceled — misreading it as a steer swallowed the
			// error and spun the loop to the iteration cap with no feedback.
			if canceled && h.steered.CompareAndSwap(true, false) {
				wr.Append(episodic.Aborted, map[string]string{"phase": "think", "iter": fmt.Sprint(i), "reason": "steered"})
				continue
			}
			// A plain context cancellation (turn aborted, request dropped) is not a
			// model failure — record it as a quiet abort, NOT a fatal error card
			// that lingers in the chat as "model call failed: context canceled".
			if canceled {
				wr.Append(episodic.Aborted, map[string]string{"phase": "think", "iter": fmt.Sprint(i), "reason": "canceled"})
				return &Result{Iterations: i - 1, StopReason: "canceled", Window: &winRep}, nil
			}
			// A tool call that blew past the token cap arrives as a server-side JSON
			// parse error ("missing closing quote"). That's not fatal — the fix is
			// smaller writes. Feed that back and let the model self-correct (max 2x).
			if isTruncatedToolCall(err) && truncated < 2 {
				truncated++
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
					"text": "tool call truncated at the token cap — asking the model to split the write"})
				correction = splitCorrection("tool")
				continue
			}
			class := classifyLLMError(err)
			wr.Append(episodic.Err, errorCard(class, "model call failed after retries", err.Error(), "3 attempts", llmFix(class)))
			return &Result{Iterations: i, StopReason: StopLLMError, Window: &winRep}, err
		}
		if len(reply.ToolCalls) == 0 {
			iterCancel()
			if _, err := wr.Append(episodic.MsgAssistant, assistantPayload(reply)); err != nil {
				return nil, err
			}
			wr.Append(episodic.TurnClose, map[string]any{"iterations": i, "pulls": len(turnPulls)})
			l.runBoundary(sessionID)
			return &Result{Reply: reply.Content, Iterations: i, StopReason: StopFinalAnswer, Pulls: len(turnPulls), Window: &winRep}, nil
		}
		if _, err := wr.Append(episodic.MsgAssistant, assistantPayload(reply)); err != nil {
			iterCancel()
			return nil, err
		}
		steered := false
		for _, tc := range reply.ToolCalls {
			args := json.RawMessage(tc.Function.Arguments)
			wr.Append(episodic.ToolCall, map[string]any{"id": tc.ID, "name": tc.Function.Name, "args": json.RawMessage(tc.Function.Arguments)})
			if !json.Valid(args) {
				// Truncated (hit the output cap) and malformed (wrong schema)
				// need OPPOSITE advice — telling a model to "regenerate" a
				// call that was simply too long makes it fail identically
				// until the error threshold kills the run.
				hint := malformedHint(tc.Function.Arguments)
				out := "malformed tool call: " + hint
				wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": false, "output": out})
				what := "malformed tool call args"
				if looksTruncated(tc.Function.Arguments) {
					what = "tool call arguments were cut off (output limit)"
				}
				wr.Append(episodic.Err, errorCard(ErrClassMalformed, what, tc.Function.Arguments, "0 retries", hint))
				// Truncated args arrive here (valid transport, cut-off JSON).
				// Feed the SAME self-correction the server-side path uses —
				// a tool result alone doesn't reliably change the model's
				// behaviour, and without this it resends until the error
				// threshold kills the run.
				if looksTruncated(tc.Function.Arguments) && truncated < 2 {
					truncated++
					wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
						"text": "tool call arguments cut off at the token cap — asking the model to split the write"})
					correction = splitCorrection(tc.Function.Name)
					continue
				}
				if detail, tripped := g.toolError(tc.Function.Name); tripped {
					iterCancel()
					return stop(&Result{Iterations: i}, StopErrors, detail), nil
				}
				continue
			}
			out, execErr := sessionReg.ExecuteMode(iterCtx, tc.Function.Name, args, mode.Name)
			if execErr != nil && iterCtx.Err() == context.Canceled && !h.killed.Load() && h.steered.CompareAndSwap(true, false) {
				wr.Append(episodic.Aborted, map[string]string{"phase": "act", "tool": tc.Function.Name, "reason": "steered"})
				steered = true
				break
			}
			ok := execErr == nil
			if execErr != nil {
				// Keep the tool's own output (stdout/stderr) — it explains WHY the
				// command failed. Overwriting it with just execErr left the model
				// blind ("exit status 1" with no context) so it retried uselessly.
				if out != "" {
					out = out + "\n" + execErr.Error()
				} else {
					out = execErr.Error()
				}
				wr.Append(episodic.Err, errorCard(ErrClassArgs, tc.Function.Name+" failed", out, "", "adjust per the hint and continue"))
				if l.recall != nil && pendingPulls == nil {
					pendingPulls = l.recall.OnError(iterCtx, sessionID, out, l.tailEvtIDs(sessionID, 20))
				}
				if detail, tripped := g.toolError(tc.Function.Name); tripped {
					wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": false, "output": out})
					iterCancel()
					return stop(&Result{Iterations: i}, StopErrors, detail), nil
				}
			} else {
				g.toolOK()
			}
			// Coach BEFORE persisting: the loop rebuilds its window from the
			// episodic log each iteration, so a hint appended here is what the
			// model actually reads next — and a model that emits several calls
			// per iteration would never see a next-turn-only correction in time.
			if g.repeatingResult(tc.Function.Name, args, out) {
				hint := repeatHint(tc.Function.Name, out)
				correction = hint
				out += "\n\n[harness] " + hint
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
					"text": "identical tool result — coaching the model to change approach"})
			}
			wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": ok, "output": out})
			// Loop detection on the RESULT: only a call that yields the SAME output
			// repeatedly is a stuck loop. Re-running e.g. `npm run build` with a
			// changing error each time is progress and must be allowed to continue.
			if detail, tripped := g.repeatedResult(tc.Function.Name, args, out); tripped {
				iterCancel()
				return stop(&Result{Iterations: i}, StopLoop, detail), nil
			}
			// One short of the kill: the model is repeating a call that keeps
			// returning the same thing. Tell it WHY and what to do instead —
			// re-reading a truncated head forever is the classic case.
			if g.repeatingResult(tc.Function.Name, args, out) {
				hint := repeatHint(tc.Function.Name, out)
				correction = hint
				// Also append it to THIS result: a model that emits several
				// calls per iteration would otherwise queue a third identical
				// one before ever seeing the next-turn correction.
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
					"text": "identical tool result — coaching the model to change approach"})
			}
		}
		iterCancel()
		if steered {
			continue
		}
	}
}

func assistantPayload(m llm.Message) map[string]any {
	p := map[string]any{"text": m.Content}
	if len(m.ToolCalls) > 0 {
		p["tool_calls"] = m.ToolCalls
	}
	return p
}

func (l *Loop) tailEvtIDs(sessionID string, n int) map[string]bool {
	events, err := episodic.Replay(l.path(sessionID))
	if err != nil {
		return nil
	}
	if len(events) > n {
		events = events[len(events)-n:]
	}
	set := map[string]bool{}
	for _, ev := range events {
		set[ev.ID] = true
	}
	return set
}

func (l *Loop) buildMessages(ctx context.Context, sessionID, systemPrompt string, pulls []memory.Pull, skillNotes []string) ([]llm.Message, window.Report, error) {
	events, err := episodic.Replay(l.path(sessionID))
	if err != nil {
		return nil, window.Report{}, err
	}
	items := []window.Item{{Msg: llm.Message{Role: "system", Content: systemPrompt}, Kind: "system"}}
	if text := memory.FormatPulls(pulls); text != "" {
		items = append(items, window.Item{Msg: llm.Message{Role: "system", Content: text}, Kind: "pulls"})
	}
	for _, note := range skillNotes {
		items = append(items, window.Item{Msg: llm.Message{Role: "system", Content: note}, Kind: "skill"})
	}
	for _, ev := range events {
		switch ev.Type {
		case episodic.MsgUser:
			var p struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil {
				items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: p.Text}, EvtID: ev.ID, Kind: "user"})
			}
		case episodic.MsgAssistant:
			var p struct {
				Text      string         `json:"text"`
				ToolCalls []llm.ToolCall `json:"tool_calls"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil {
				// SANITIZE replayed tool calls: a truncated call (cut at the token
				// cap) has invalid JSON args. Sent back verbatim, llama.cpp fails to
				// RENDER the chat template and every subsequent request in the
				// session dies instantly with the same parse error — the session is
				// self-poisoned. Stub bad args to {} so history always renders; the
				// paired tool_result already tells the model the call was malformed.
				for ti := range p.ToolCalls {
					if !json.Valid([]byte(p.ToolCalls[ti].Function.Arguments)) {
						p.ToolCalls[ti].Function.Arguments = "{}"
					}
				}
				items = append(items, window.Item{Msg: llm.Message{Role: "assistant", Content: p.Text, ToolCalls: p.ToolCalls}, EvtID: ev.ID, Kind: "assistant"})
			}
		case episodic.ToolResult:
			var p struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Output string `json:"output"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil {
				// Ingress cap: episodic keeps the raw output (source of truth);
				// the WINDOW only ever sees the per-tool capped head. Full result
				// is recallable from the episodic log via its evt id.
				content := tools.CapIngress(p.Output, l.tools.IngressCapFor(p.Name))
				items = append(items, window.Item{Msg: llm.Message{Role: "tool", ToolCallID: p.ID, Content: content}, EvtID: ev.ID, Kind: "tool"})
			}
		}
	}
	if l.win == nil {
		msgs := []llm.Message{}
		for _, it := range items {
			msgs = append(msgs, it.Msg)
		}
		return msgs, window.Report{}, nil
	}
	msgs, rep := l.win.Build(ctx, items)
	return msgs, rep, nil
}

// longTurnKey marks a turn as a supervised plan step (RFX_UI planner):
// a build task that gets maxStepTime instead of the chat budget.
type longTurnKey struct{}

// WithLongTurn returns a context whose turn runs on the long (step) budget.
func WithLongTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, longTurnKey{}, true)
}

func turnBudget(ctx context.Context) time.Duration {
	if v, _ := ctx.Value(longTurnKey{}).(bool); v {
		return maxStepTime
	}
	return maxTurnTime
}

// compress runs in-memory items through the window manager. buildMessages
// replays from the episodic log; a plan step already holds its items, so it
// needs the compression step alone.
func (l *Loop) compress(ctx context.Context, items []window.Item) ([]llm.Message, window.Report) {
	if l.win == nil {
		msgs := make([]llm.Message, 0, len(items))
		for _, it := range items {
			msgs = append(msgs, it.Msg)
		}
		return msgs, window.Report{}
	}
	return l.win.Build(ctx, items)
}
