package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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
	maxStepTime   = 20 * time.Minute
	maxTurnTokens = 16384
	// A turn that exhausts its token budget is checkpointed and continued with
	// a fresh slice, this many times, before the guard finally stops it. Big
	// multi-file builds routinely need 2-3 slices; a runaway loop still dies.
	maxTokenExtensions = 3
	// Same doctrine for the iteration cap: a progressing turn earns more
	// slices; a spinning one is caught by the repeat/idle/error guards.
	maxIterExtensions = 3
	loopDetectRepeat  = 3
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
	thinkMu      sync.RWMutex
	thinkMode    string               // off | autopilot | always
	thinkEffort  string               // low | medium | xhigh
	stackInfo    func() string        // the harness's own running services + reserved ports
	isInstant    func(id string) bool // is this session an ephemeral instant session?
	bg           sync.WaitGroup
	regFor       func(ws string) *tools.Registry
}

// SetWorkspaceFunc wires a live getter for the active workspace path so the
// system prompt can tell the model where it actually is.
func (l *Loop) SetWorkspaceFunc(f func(sessionID string) string) { l.workspace = f }

// SetRegistryForWorkspace wires a builder that produces a tool registry rooted
// in a GIVEN workspace.
//
// Without it, every session shares one registry built at startup from the
// global cfg.Workspace — and each tool captures its jail root at CONSTRUCTION
// (NewServe(ws), file jails), so no amount of SetWorkspace on the registry can
// redirect them afterwards.
//
// The result was a harness that lied: envBlock told the model "your workspace
// is <session path>. All file tools are rooted here", while the tools actually
// read, wrote and served the core's global workspace. A benchmark session
// pointed at ~/Pictures/Benchmark had its static server serve an unrelated
// chess project, and a regex search miss a function that was sitting in its
// real workspace.
func (l *Loop) SetRegistryForWorkspace(f func(ws string) *tools.Registry) {
	l.toolsMu.Lock()
	l.regFor = f
	l.toolsMu.Unlock()
}

// registryFor returns the registry a session's tools must run in: one rooted in
// that session's own workspace when a builder is wired, else the global one.
func (l *Loop) registryFor(sessionID string) *tools.Registry {
	l.toolsMu.RLock()
	f, ws := l.regFor, l.workspace
	l.toolsMu.RUnlock()
	if f == nil || ws == nil {
		return l.registry()
	}
	path := ws(sessionID)
	if path == "" {
		return l.registry()
	}
	if r := f(path); r != nil {
		return r
	}
	return l.registry()
}

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
	// Every tool call under this turn belongs to THIS session, whatever the
	// shared SessionContext says by the time it runs.
	ctx = tools.WithSession(ctx, sessionID)
	mode := ModeByName(modeName)
	systemPrompt := basePrompt + l.envBlock(sessionID) + "\n\n" + ReminderGuidance + "\n\n" + mode.Module
	// In autopilot, a plan committed earlier (in Discussion) is injected as GUIDANCE
	// — the agent follows its intent but adapts freely. No plan is fine: it plans
	// and executes from the task directly.
	var activePlan *Plan
	if mode.Name == "autopilot" {
		if plan, _, perr := LatestPlan(l.path(sessionID)); perr == nil && plan != nil {
			// An UNFINISHED plan owns the session. "keep going" typed into
			// the chat used to start a free turn with the plan pasted in as
			// guidance — the old one-turn path, never the supervisor — and
			// the model replayed the exact reply it had been stuck on, six
			// times (NFQ, 2026-09-05). Resume the plan instead: the next
			// pending step, or the blocked one reopened, with the user's
			// words as steering. A finished plan does not capture the turn.
			if sup, serr := l.restoreSupervisor(sessionID, plan); serr == nil && !sup.Done() {
				return l.handOffToPlan(ctx, sessionID, plan, nil, userMsg)
			}
			systemPrompt += "\n\n" + plan.AsGuidance()
			activePlan = plan
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
	lastCompacted := 0
	breaker := newBashBreaker()
	var work *workTracker
	if l.workspace != nil {
		work = newWorkTracker(l.workspace(sessionID))
	}
	var turnPulls, pendingPulls []memory.Pull
	if l.recall != nil {
		turnPulls = l.recall.TurnStart(ctx, sessionID, userMsg, l.tailEvtIDs(sessionID, 20))
	}
	sessionReg := l.registryFor(sessionID)
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
	// Thinking follows the MODE: a build in autopilot may reason before each
	// call; a chat turn answers directly. The level travels with the context,
	// so every model call under this turn — retries included — sees it.
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
	// bumpSampling breaks the determinism that keeps a repeat repeating: at the
	// session default of 0.2 the same history yields the same 1400-token tool
	// call, hint or no hint (three byte-identical evals in 34 s, 2026-09-04).
	// One call at the creative preset after a repeat is detected gives the
	// coaching an actual chance to change the output.
	bumpSampling := false
	thinkLevel := l.thinkingFor(mode.Name) // steps DOWN when reasoning overflows: xhigh → medium → low → off
	emptyRetried := false                  // one empty reply gets one request for an answer
	// PLAN FIRST. An autopilot turn with no committed plan spends its first
	// call dividing the task: only commit_plan is offered, thinking gets the
	// planning budget, and the instruction is to size steps to what a few
	// tool calls can do. The Crane run (2026-09-04) spent 16k tokens
	// reasoning about the whole game before its first action and never got
	// to act; a plan of small steps is how a 27B model builds a big thing.
	planFirst := mode.Name == "autopilot" && activePlan == nil
	planReads := 0        // read-only calls spent looking before committing a plan
	planRejects := 0      // commit_plan calls Validate refused (a check that cannot fail)
	planAsked := false    // the instruction was appended once
	planInsisted := false // prose instead of a plan: insisted once (the gate and its budget stay on for that retry)
	for i := 1; ; i++ {
		if h.killed.Load() {
			wr.Append(episodic.Aborted, map[string]string{"phase": "turn", "reason": "killed by user"})
			return stop(&Result{Iterations: i - 1}, "killed", "killed by user — state preserved in episodic"), nil
		}
		if h.paused.Load() {
			return &Result{Iterations: i - 1, StopReason: "paused", Reply: "paused — resume anytime, the log is the state", Window: &winRep}, nil
		}
		// Token exhaustion is a checkpoint, not a death: grant a fresh slice
		// (bounded) and tell the model to CONTINUE — its work so far is in the
		// episodic log and the window rebuild compresses it back in.
		if g.tokensExhausted() && g.extendTokens() {
			wr.Append(episodic.Note, map[string]string{"kind": "token_checkpoint",
				"text": "token budget checkpoint — budget refreshed; continue the work already in progress, do not restart it"})
		}
		if reason, detail, tripped := g.preThink(i); tripped {
			// The iteration cap measures effort, not stuckness — a turn that is
			// still making real progress earns another slice (bounded); the
			// repeat/idle/error guards catch genuine spinning.
			if reason == StopIterations && g.extendIter() {
				wr.Append(episodic.Note, map[string]string{"kind": "iteration_checkpoint",
					"text": "iteration checkpoint — cap raised for a progressing turn; continue the work in progress"})
			} else {
				return stop(&Result{Iterations: i - 1}, reason, detail), nil
			}
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
		// Surface compaction the moment it happens. A user watching a long run
		// needs to know history was folded away — otherwise the model
		// "forgetting" an earlier instruction looks like the model ignoring it.
		if rep.Compacted > 0 && rep.Compacted != lastCompacted {
			wr.Append(episodic.Note, map[string]string{
				"kind": "context_compacted",
				"text": fmt.Sprintf("context compacted — %d earlier turns folded away to stay under the %d-token window (full log kept on disk)", rep.Compacted, rep.Budget),
			})
			lastCompacted = rep.Compacted
		}
		// Churn detection. Errors are the wrong signal — the two worst runs of
		// the benchmark had almost none while producing nothing. Ask instead
		// whether the workspace moved.
		//
		// A NUDGE, not a kill: "nothing changed" is also what a finished task
		// looks like, and killing a model that is about to report success would
		// be worse than the churn. Say it, and let the model decide.
		if work != nil {
			detail, stuck := work.check()
			g.observeWorkspace(workFP(work))
			if work.changed {
				g.progress() // the artifact moved: the only progress that cannot be faked
			}
			if stuck && work.stopping {
				iterCancel()
				wr.Append(episodic.Note, map[string]string{"kind": "no_progress", "text": detail})
				return stop(&Result{Iterations: i - 1}, StopStalled, detail), nil
			}
			if stuck {
				correction = detail + " Stop repeating the current approach: either " +
					"change tactics, or if the work is actually complete, say so and finish."
				wr.Append(episodic.Note, map[string]string{"kind": "no_progress",
					"text": "no workspace change in " + fmt.Sprint(progressStallLimit) + " iterations — nudging the model to change approach or finish"})
			}
		}
		winRep = rep
		callCtx := iterCtx
		specs := sessionReg.Specs(mode.Name)
		callLevel := thinkLevel
		if planFirst && len(onlyTool(specs, "commit_plan")) == 0 {
			// The tool the gate exists to ask for is not offered in this
			// mode. That is a registration bug (see cmd/crv/main.go), and
			// it must be loud: fenced to discussion only, every autopilot
			// planning call offered an empty tool list and the model's
			// prose plans were blamed on the model. Forcing tool_choice on
			// an empty list is a 400 from vLLM, so do neither — say so and
			// run unplanned.
			planFirst = false
			wr.Append(episodic.Note, map[string]string{"kind": "plan_first",
				"text": "commit_plan is not available in " + mode.Name + " mode — check its Modes in the tool registry; proceeding without a plan"})
		}
		if planFirst {
			// "Improve our car game" cannot be planned without reading the
			// car game. The gate used to offer commit_plan and nothing else,
			// so the model either planned blind or — as on the car
			// restructure — reasoned itself into a corner ("I need to see
			// the files first… but the instruction says plan now"), emitted
			// nothing, and the gate gave up. Reading is not doing the work;
			// it is what a plan is made from. A few read-only calls are
			// allowed, then it is commit_plan only so planning cannot become
			// an unbounded tour of the tree.
			// Once reading is spent, or the model has already failed to
			// commit once, the call is FORCED to commit_plan. Narrowing the
			// offered specs alone was toothless: the model calls tools it
			// remembers from the instruction text, and the registry runs
			// them. Guided decoding is the only thing that binds it.
			if planReads < maxPlanReads && !planInsisted {
				specs = onlyTools(specs, planningTools...)
			} else {
				specs = onlyTool(specs, "commit_plan")
				callCtx = llm.WithForcedTool(callCtx, "commit_plan")
			}
			if !planInsisted {
				callLevel = l.planThinkingFor(mode.Name)
			}
			if !planAsked {
				planAsked = true
				messages = append(messages, llm.Message{Role: "user", Content: planInstruction})
				wr.Append(episodic.Note, map[string]string{"kind": "plan_first",
					"text": "no plan yet — first call divides the task into steps (read-only tools + commit_plan)"})
			}
			if callLevel != llm.ThinkingOff {
				callCtx = llm.WithThinkingBudget(callCtx, planThinkingBudget)
			}
		}
		if bumpSampling {
			callCtx = WithSampling(callCtx, "creative")
			bumpSampling = false
			wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
				"text": "sampling raised to creative for one call — a repeat at strict temperature reproduces itself"})
		}
		reply, usage, err := l.completeWithRetry(llm.WithThinking(callCtx, callLevel), wr, messages, specs, "", mode.ProseCap)
		g.addTokens(usage.AnswerTokens()) // reasoning is not re-sent: it costs time, not window
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
		// A reply with nothing in it is not a final answer. Two shapes:
		// (1) finish_reason=length with empty text — the model was still
		// thinking when the cap hit; the reasoning is kept for the record and
		// the same call is made again WITHOUT thinking, once. (2) empty text
		// with no cut-off — ask once for the answer, then give up honestly.
		if len(reply.ToolCalls) == 0 && strings.TrimSpace(reply.Content) == "" && !planFirst {
			wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage))
			if reply.Raw != "" {
				wr.Append(episodic.Note, map[string]string{"kind": "empty_reply_raw", "text": reply.Raw})
			}
			if reply.Truncated() && thinkLevel != llm.ThinkingOff {
				next := llm.StepDown(thinkLevel)
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
					"text": fmt.Sprintf("thinking at %s ran past its budget (%d reasoning tokens, no answer) — retrying this step at %s", thinkLevel, usage.ReasoningTokens, next)})
				thinkLevel = next
				iterCancel()
				continue
			}
			if !emptyRetried {
				emptyRetried = true
				correction = "Your last reply was empty — no text and no tool call. Either call a tool to continue the work, or write the answer."
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct", "text": "empty reply — asking for the answer or a tool call"})
				iterCancel()
				continue
			}
			iterCancel()
			return stop(&Result{Iterations: i}, StopLLMError, "the model returned two empty replies in a row"), nil
		}
		if len(reply.ToolCalls) > 0 || strings.TrimSpace(reply.Content) != "" {
			emptyRetried = false // a real reply closes an empty streak
		}
		if len(reply.ToolCalls) == 0 && planFirst {
			if reply.Truncated() && thinkLevel != llm.ThinkingOff {
				// still thinking at the cap: same graded fallback as any step
				wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage))
				next := llm.StepDown(thinkLevel)
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
					"text": fmt.Sprintf("planning at %s ran past its budget (%d reasoning tokens) — retrying the plan at %s", thinkLevel, usage.ReasoningTokens, next)})
				thinkLevel = next
				iterCancel()
				continue
			}
			// A plan written as XML-style text is a tool call the parser
			// missed: translate it, commit it, and move on to step 1.
			if p, note := planFromText(wr, reply.Content); p != nil {
				wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage))
				wr.Append(episodic.Note, map[string]string{"kind": "plan_first",
					"text": fmt.Sprintf("plan written as text — translated and committed (%d steps): %s", len(p.Steps), note)})
				iterCancel()
				return l.handOffToPlan(runCtx, sessionID, p, wr, "")
			}
			// Not every autopilot turn is a build. "Who was president in 1950"
			// is answered, not planned, and the gate used to throw that answer
			// away: the model replied "Harry S. Truman…", the gate saw prose
			// instead of a tool call, asked twice more, and returned the
			// model's third-round "no plan to commit" as the reply. A correct
			// answer was lost to a gate that only wanted a plan (2026-09-04).
			//
			// So: an answer that is not a plan attempt IS the turn. Recognise
			// it before insisting, and finish.
			if answersWithoutAPlan(reply.Content) {
				wr.Append(episodic.Note, map[string]string{"kind": "plan_first",
					"text": "answered directly — nothing here needs a plan"})
				iterCancel()
				if _, err := wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage)); err != nil {
					return nil, err
				}
				wr.Append(episodic.TurnClose, map[string]any{"iterations": i, "pulls": len(turnPulls)})
				l.runBoundary(sessionID)
				return &Result{Reply: reply.Content, Iterations: i, StopReason: StopFinalAnswer,
					Pulls: len(turnPulls), Window: &winRep}, nil
			}
			// Prose, or an empty reply (a tool call the server's parser
			// swallowed — every planning call WITH thinking did one or the
			// other today, every build step WITHOUT thinking called tools
			// fine). Once: insist, and retry the plan without thinking.
			// Twice: run without a plan rather than argue.
			wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage))
			if reply.Raw != "" {
				wr.Append(episodic.Note, map[string]string{"kind": "empty_reply_raw", "text": reply.Raw})
			}
			if planInsisted {
				planFirst = false
				wr.Append(episodic.Note, map[string]string{"kind": "plan_first", "text": "no plan committed after two asks — proceeding without one"})
			} else {
				planInsisted = true
				thinkLevel = llm.ThinkingOff
				correction = "That was not a tool call. Call the commit_plan tool now with the steps (title, files) — a plan in prose cannot be tracked. No preamble: the tool call is the whole reply."
				wr.Append(episodic.Note, map[string]string{"kind": "plan_first", "text": "no tool call for the plan — asking once more, without thinking"})
			}
			iterCancel()
			continue
		}
		if len(reply.ToolCalls) == 0 {
			iterCancel()
			if _, err := wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage)); err != nil {
				return nil, err
			}
			wr.Append(episodic.TurnClose, map[string]any{"iterations": i, "pulls": len(turnPulls)})
			l.runBoundary(sessionID)
			return &Result{Reply: reply.Content, Iterations: i, StopReason: StopFinalAnswer, Pulls: len(turnPulls), Window: &winRep}, nil
		}
		if _, err := wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage)); err != nil {
			iterCancel()
			return nil, err
		}
		// The model's words, before its calls. Saying the identical thing
		// three times is a loop whatever the calls around it look like.
		if hint, kill := g.sawText(reply.Content); kill {
			iterCancel()
			return stop(&Result{Iterations: i}, StopLoop, hint), nil
		} else if hint != "" {
			correction = hint
			wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
				"text": "identical reply twice — coaching the model to change approach"})
		}
		// A reply with tool calls is NOT progress by itself: a model calling
		// tools that keep failing was "progressing" all the way to a 20-minute
		// loop. The idle clock now restarts only on a successful result the
		// turn has not seen before (below), or on a workspace change (above).
		// The budget is idle time, not total time, so a slow generation that
		// then succeeds still resets it.
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
				// Same reasoning as a failed tool: the model is corrected in-band,
				// the turn continues, so this is a note in the log, not a card.
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct", "text": what + " — " + hint})
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
				// Bash failing the same WAY three times is not three problems,
				// it is one wall the model keeps walking into with a new
				// command each time. Make it stop and ask whether the thing
				// exists at all, rather than counting it toward a kill.
				if tc.Function.Name == "bash" {
					var ba struct {
						Command string `json:"command"`
					}
					_ = json.Unmarshal(args, &ba)
					if hint, tripped := breaker.record(ba.Command, out); tripped {
						out += "\n\n[harness] " + hint
						correction = hint
						wr.Append(episodic.Note, map[string]string{"kind": "breaker_tripped",
							"text": "same bash failure 3× — asking the model to reconsider the approach"})
					}
				}
				if detail, tripped := g.toolError(tc.Function.Name, out); tripped {
					iterCancel()
					return stop(&Result{Iterations: i}, StopErrors, detail), nil
				}
				continue
			}
			// An identical check_page / read of a workspace nothing has touched
			// since cannot answer differently. Hand back the previous result
			// with the hint instead of spending six seconds of Chromium to
			// learn it again. Bash is never short-circuited: `date`, polls and
			// servers are allowed to change.
			var out string
			var execErr error
			// Set when the result came from the cache. The hint appended below
			// makes `out` a string the turn has never seen, so seenBefore()
			// says "new" for what is definitionally a repeat — and anything
			// keyed on novelty (the idle clock, the stall credit) would treat
			// a model re-asking the same question as progress, forever.
			cached := false
			if prev, ok := g.cachedPureResult(tc.Function.Name, args, workFP(work)); ok {
				cached = true
				out = prev + "\n\n[harness] not re-run: identical call, workspace unchanged since — this is the same result you already have."
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
					"text": tc.Function.Name + " not re-run — identical call on an unchanged workspace"})
			} else {
				out, execErr = sessionReg.ExecuteMode(iterCtx, tc.Function.Name, args, mode.Name)
				if execErr == nil {
					g.rememberPureResult(tc.Function.Name, args, workFP(work), out)
				}
			}
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
				// No error CARD for a failure the model is about to correct: the
				// failed tool row in the working log already shows what happened,
				// the model gets the full output as its result, and the turn goes
				// on. A card with RETRY / DISMISS under a run that keeps working
				// asked the user to act on something nobody needed them for. The
				// card is for when the turn actually ENDS on errors (stop() below).
				if l.recall != nil && pendingPulls == nil {
					pendingPulls = l.recall.OnError(iterCtx, sessionID, out, l.tailEvtIDs(sessionID, 20))
				}
				if detail, tripped := g.toolError(tc.Function.Name, out); tripped {
					wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": false, "output": out})
					iterCancel()
					return stop(&Result{Iterations: i}, StopErrors, detail), nil
				}
			} else {
				if tc.Function.Name == "bash" {
					var ba struct {
						Command string `json:"command"`
					}
					if json.Unmarshal(args, &ba) == nil {
						breaker.ok(ba.Command)
					}
				}
				g.toolOK(tc.Function.Name)
			}
			// Coach BEFORE persisting: the loop rebuilds its window from the
			// episodic log each iteration, so a hint appended here is what the
			// model actually reads next — and a model that emits several calls
			// per iteration would never see a next-turn-only correction in time.
			if g.repeatingResult(tc.Function.Name, args, out) {
				hint := repeatHint(tc.Function.Name, out)
				correction = hint
				bumpSampling = true
				out += "\n\n[harness] " + hint
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
					"text": "identical tool result — coaching the model to change approach"})
			}
			// Real work happened: restart the IDLE clock — but only for a
			// SUCCESSFUL result this turn has not seen before. A failure, or
			// the same output again, is standing still, and standing still is
			// exactly what the idle guard exists to catch.
			if ok && !cached && !g.seenBefore(tc.Function.Name, args, out) {
				g.progress()
				// Verification is work, and it does not touch the workspace.
				// The stall counter measures file changes only, so a build
				// that finished writing and moved on to checking its own
				// output looked identical to a model circling: the fan run
				// wrote three files, then was killed 45 seconds later for
				// "8 iterations with no change" while running the checks the
				// prompt had explicitly asked for (2026-09-04). A NEW,
				// SUCCESSFUL result is progress wherever it came from; a
				// repeat or a failure still is not, and still counts.
				if work != nil {
					work.credit()
				}
			}
			// The model's stubbornest habit: writing its plan to a .md file
			// instead of calling commit_plan. Translate, don't plead — a
			// plan-shaped write is ALSO committed as a structured plan event,
			// so the plan card and the Planner see it. Disclosed.
			if ok && activePlan == nil {
				if p, note := autoCommitPlanFile(wr, tc.Function.Name, args); p != nil {
					activePlan = p
					out += "\n\n[harness] " + note
				}
			}
			// Surface architecture drift: a write outside the plan's declared
			// files gets a visible note (appended BEFORE persisting, same
			// reasoning as the repeat coaching above).
			if note := outOfPlanNote(activePlan, tc.Function.Name, args); note != "" {
				out += "\n\n[harness] " + note
			}
			wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": ok, "output": out})
			// The same result for DIFFERENT inputs: the input is not the problem.
			// Only results that carry an error count — a successful edit answers
			// every call with the same confirmation, and six of those in a row
			// ended a run that was debugging its physics (Crane5, 2026-09-04).
			if hint, kill := g.sameResultAgain(tc.Function.Name, out, ok); kill {
				iterCancel()
				return stop(&Result{Iterations: i}, StopLoop, hint), nil
			} else if hint != "" {
				correction = hint
				bumpSampling = true
				wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
					"text": "same result for different inputs — telling the model the input is not the problem"})
			}
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
				bumpSampling = true
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
		if planFirst {
			// A plan landed: it becomes the turn's guide from the next call
			// on. Otherwise the calls were reads, and the gate stays open —
			// the model looked, and its next call is still the planning
			// call. Bounded by maxPlanReads above.
			if plan, _, perr := LatestPlan(l.path(sessionID)); perr == nil && plan != nil {
				// A plan landed. Do NOT carry on as one long turn with the
				// plan pasted in as guidance — that is the decoration the
				// whole design replaces. Hand the plan to the supervisor:
				// one run per step, each verified by its own check, each
				// recorded as a checkpoint. The chat turn ends with that
				// report.
				iterCancel()
				return l.handOffToPlan(runCtx, sessionID, plan, wr, "")
			} else {
				// No plan landed. Either the calls were reads (count them,
				// minus orientation), or commit_plan ran and Validate refused
				// the plan — the tool result carries the reason, and the next
				// call is forced to try again. Twice refused is the model
				// unable to write a check that can fail; go on without a plan
				// rather than loop on it.
				committed, counted := false, false
				for _, tc := range reply.ToolCalls {
					switch {
					case tc.Function.Name == "commit_plan":
						committed = true
					case !planOrientation[tc.Function.Name]:
						counted = true
					}
				}
				switch {
				case committed:
					planRejects++
					planInsisted = true // the next call is forced
					if planRejects >= maxPlanRejects {
						planFirst = false
						wr.Append(episodic.Note, map[string]string{"kind": "plan_first",
							"text": fmt.Sprintf("commit_plan refused %d times (see the tool results) — proceeding without a plan", planRejects)})
					} else {
						wr.Append(episodic.Note, map[string]string{"kind": "plan_first",
							"text": fmt.Sprintf("commit_plan refused (%d/%d) — the tool result says why; asking again, forced", planRejects, maxPlanRejects)})
					}
				default:
					if counted {
						planReads++
					}
					wr.Append(episodic.Note, map[string]string{"kind": "plan_first",
						"text": fmt.Sprintf("read before planning (%d/%d reads used) — still expecting commit_plan", planReads, maxPlanReads)})
				}
			}
		}
	}
}

// planThinkingBudget is the reasoning room for the planning call: it happens
// once and is where deep thinking pays; steps get the level's normal budget.
const planThinkingBudget = 24576

const planInstruction = "Divide this task into steps and commit them with the commit_plan TOOL (a real tool call, not text). " +
	"If the task is about EXISTING code, read it first — glob, read, grep, file_map are available now — a plan for code you have not seen is a guess. " +
	"Then commit. Keep your reasoning brief — the steps are the output. " +
	"Size each step to what a few tool calls can finish: ONE file or one concern per step, at most ~200 lines " +
	"written per step, verification (check_page / a test run) as its own step near the end. Name each step's " +
	"files. Do not write code in this call — commit the plan, then the next call starts step 1."

// handOffToPlan runs a plan that just landed, step by step, and ends the chat
// turn with the supervisor's report.
//
// Before this, a committed plan was appended to the system prompt as guidance
// and the SAME turn carried on: one long loop, the model trusted to touch
// every step, nothing verified, no checkpoint written. The improve-ctx run
// committed a ten-step plan with real checks and then wrote five modules in
// one unbroken turn with zero checkpoints. The supervisor exists for exactly
// this moment.
func (l *Loop) handOffToPlan(ctx context.Context, sessionID string, plan *Plan, wr *episodic.Writer, steer string) (*Result, error) {
	if wr == nil {
		w, err := l.open(sessionID)
		if err != nil {
			return nil, err
		}
		wr = w
	}
	// Restore the cursor from the log: a fresh plan has no checkpoints and
	// starts at step 1; a resumed one carries what already passed.
	sup, err := l.restoreSupervisor(sessionID, plan)
	if err != nil {
		return nil, err
	}
	if b := sup.Blocked(); b >= 0 && steer != "" {
		// the user asked to continue past a blocked step: one more run
		sup.Steps[b].Status = "pending"
		sup.Steps[b].Attempts = 0
	}
	if steer != "" {
		wr.Append(episodic.Note, map[string]string{"kind": "plan_first",
			"text": fmt.Sprintf("resuming the committed plan at step %d — the user said: %s", sup.Next()+1, clipEvidence(steer))})
	} else {
		wr.Append(episodic.Note, map[string]string{"kind": "plan_first",
			"text": fmt.Sprintf("plan committed: %d steps — handing off to step-by-step execution", len(plan.Steps))})
	}
	return l.runPlanFrom(ctx, sessionID, plan, sup, sup.Next(), false, steer)
}

// planningTools are what a plan may be made FROM: the code-reading set plus
// commit_plan itself. Deliberately not RiskSafe, which also holds serve,
// web_fetch, ask_user and remember — all side effects, none of them reading.
var planningTools = []string{
	"commit_plan", "read", "glob", "grep", "file_map", "find_symbol", "find_references", "outline_file",
}

// maxPlanReads bounds how many READS planning may spend. Orientation calls —
// file_map and glob — are one-shot and do not count: on the car restructure
// they ate two of three slots, the one read that followed returned 208 of 435
// lines, and the model was refused the second read it asked for ("Let me read
// the rest of the file") and stopped with no plan. Four reads covers a large
// file in chunks plus a grep; a fifth means touring, and it gets commit_plan
// alone.
const maxPlanReads = 4

// maxPlanRejects is how many refused commit_plan calls planning tolerates.
// The tool result names the offending check each time. Live, the model
// mislabelled a check on try one, dressed existence up as a command on try
// two, and wrote real content checks on try three — after the budget of two
// had already given up (2026-09-05). Three forced calls is ~30 s; an
// unplanned run is the whole design lost.
const maxPlanRejects = 3

// planOrientation are the planning calls that map the tree rather than read a
// file. Each is naturally one-shot, so they are free.
var planOrientation = map[string]bool{"file_map": true, "glob": true}

// onlyTools keeps the named tools' specs, in registry order.
func onlyTools(specs []llm.ToolSpec, names ...string) []llm.ToolSpec {
	keep := make(map[string]bool, len(names))
	for _, n := range names {
		keep[n] = true
	}
	out := specs[:0:0]
	for _, sp := range specs {
		if keep[sp.Function.Name] {
			out = append(out, sp)
		}
	}
	return out
}

// onlyTool keeps the named tool's spec, so a call can be constrained to it.
func onlyTool(specs []llm.ToolSpec, name string) []llm.ToolSpec {
	out := specs[:0:0]
	for _, sp := range specs {
		if sp.Function.Name == name {
			out = append(out, sp)
		}
	}
	return out
}

// assistantPayload records what the model said AND what the call cost.
//
// Usage was parsed from every response, handed to the turn guard, and then
// dropped — so the episodic log had no token counts at all, and questions like
// "which Core reached a working answer for fewer tokens" could not be answered
// from the record however many benchmarks were run. It is written now.
//
// Cached tokens are kept separate from the prompt total. With prefix caching a
// 31k prompt that is 28k cache read is nothing like a 31k prompt sent fresh,
// and one number describes both.
func assistantPayload(m llm.Message, u llm.Usage) map[string]any {
	p := map[string]any{"text": m.Content}
	if m.Reasoning != "" {
		p["reasoning"] = m.Reasoning
	}
	if len(m.ToolCalls) > 0 {
		p["tool_calls"] = m.ToolCalls
	}
	// omit entirely when the server reported nothing, so absent stays
	// distinguishable from zero
	if u.PromptTokens > 0 || u.CompletionTokens > 0 {
		usage := map[string]any{
			"prompt_tokens":     u.PromptTokens,
			"completion_tokens": u.CompletionTokens,
		}
		if u.ReasoningTokens > 0 {
			usage["reasoning_tokens"] = u.ReasoningTokens
		}
		if u.CachedTokens > 0 {
			usage["cached_tokens"] = u.CachedTokens
			usage["fresh_prompt_tokens"] = u.FreshPromptTokens()
		}
		p["usage"] = usage
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
	// If the packer has to drop history, it replaces it with THIS rather than a
	// bare "turns were removed" note: the original request, the plan, finished
	// work, and what is on disk. Assembled from the log, so a model that just
	// lost its context is never asked to summarise what it lost.
	if l.win != nil {
		l.win.SetResumeBrief(func(n int) string {
			return buildResumeBrief(l.resumeFacts(sessionID, events, n))
		})
	}
	// User-role, not system-role. The model's chat template permits exactly ONE
	// system message, at index 0 — a second is rejected even at the front
	// (verified against the live endpoint AND vllm docs: placement rules live
	// in the model's template; vLLM's own chat client sends a single
	// --system-prompt). Injected context therefore ships as user-role
	// <system-reminder> text, the qwen-code / deepseek-harness convention.
	if text := wrapReminder(memory.FormatPulls(pulls)); text != "" {
		items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "pulls"})
	}
	for _, note := range skillNotes {
		if text := wrapReminder(note); text != "" {
			items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "skill"})
		}
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
type samplingKey struct{}

// WithSampling carries a ONE-TURN sampling override — the chat bar asking for
// this message to run hotter or tighter, without changing the session default
// everything else uses. Empty means "use the default".
func WithSampling(ctx context.Context, preset string) context.Context {
	if preset == "" {
		return ctx
	}
	return context.WithValue(ctx, samplingKey{}, preset)
}

func samplingOf(ctx context.Context) string {
	v, _ := ctx.Value(samplingKey{}).(string)
	return v
}

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

// resumeFacts pulls the non-recoverable context out of the episodic log: the
// user's first request, the committed plan, and completed checkpoints. Files
// come from the workspace itself, so the list is what is actually there now.
func (l *Loop) resumeFacts(sessionID string, events []episodic.Event, compacted int) resumeFacts {
	f := resumeFacts{Compacted: compacted}
	st := episodic.Fold(events)

	for _, ev := range st.Messages {
		if ev.Type != episodic.MsgUser {
			continue
		}
		var p struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil && strings.TrimSpace(p.Text) != "" {
			f.Goal = p.Text // the FIRST user message is the request
			break
		}
	}
	if len(st.Plan) > 0 {
		var pl struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(st.Plan, &pl) == nil {
			f.Plan = pl.Title
		}
	}
	for _, ev := range events {
		if ev.Type != episodic.Checkpoint {
			continue
		}
		var cp struct {
			Step, Status, Summary string
		}
		if json.Unmarshal(ev.Payload, &cp) == nil && cp.Status == "done" {
			line := cp.Step
			if cp.Summary != "" {
				line += " — " + cp.Summary
			}
			f.Done = append(f.Done, line)
		}
	}
	if l.workspace != nil {
		if ws := l.workspace(sessionID); ws != "" {
			f.Workspace = ws
			f.Files = workspaceFiles(ws)
		}
	}
	return f
}

// SetThinking sets which modes think and how hard. Live: it is a per-request
// template argument, so the next call under an autopilot turn already gets it.
func (l *Loop) SetThinking(mode, effort string) {
	l.thinkMu.Lock()
	defer l.thinkMu.Unlock()
	switch mode {
	case "off", "plan", "autopilot", "always":
	default:
		mode = "plan" // the default: think while dividing the task, not while writing the steps
	}
	if !llm.ValidThinking(effort) || effort == llm.ThinkingOff {
		effort = llm.ThinkingLow
	}
	l.thinkMode, l.thinkEffort = mode, effort
}

// Thinking reports the setting: mode and effort.
func (l *Loop) Thinking() (mode, effort string) {
	l.thinkMu.RLock()
	defer l.thinkMu.RUnlock()
	mode, effort = l.thinkMode, l.thinkEffort
	if mode == "" {
		mode = "plan"
	}
	if effort == "" {
		effort = llm.ThinkingLow
	}
	return mode, effort
}

// thinkingFor resolves the level for a turn's EXECUTION calls from its mode.
// "plan" thinks only in the planning call (see planThinkingFor): measured
// three times on 2026-09-04, the planning call reasons ~500 tokens and
// yields a good plan, while every code-writing call reasons ~10k at "low"
// and overflows — thinking helps divide the work, not write it.
func (l *Loop) thinkingFor(modeName string) string {
	mode, effort := l.Thinking()
	switch {
	case mode == "always":
		return effort
	case mode == "autopilot" && modeName == "autopilot":
		return effort
	}
	return llm.ThinkingOff
}

// planThinkingFor is the level for the planning call of an autopilot turn.
func (l *Loop) planThinkingFor(modeName string) string {
	mode, effort := l.Thinking()
	if modeName == "autopilot" && (mode == "plan" || mode == "autopilot" || mode == "always") {
		return effort
	}
	return l.thinkingFor(modeName)
}

// SetSampling changes the session's default sampling preset, live. Temperature
// is a per-request field; it never required a restart, only a way to say so.
func (l *Loop) SetSampling(name string) {
	if l.llm != nil {
		l.llm.SetSampling(name)
	}
}

func (l *Loop) SamplingName() string {
	if l.llm == nil {
		return "strict"
	}
	return l.llm.SamplingName()
}

// workFP is the workspace fingerprint the tracker computed at the top of this
// iteration, or 0 when no workspace is tracked (then nothing is ever cached).
func workFP(w *workTracker) uint64 {
	if w == nil {
		return 0
	}
	return w.last
}

// answersWithoutAPlan reports whether a reply to the plan gate is an ANSWER
// rather than a failed attempt at a plan.
//
// The gate asks the first call of an autopilot turn to divide the task into
// steps. That is right for a build and wrong for a question: "who was
// president in 1950" has an answer, not a plan. Before this, such a reply was
// treated as a missed tool call — the answer was discarded, the model was
// asked twice more, and its "there is nothing to plan" became the user's
// reply.
//
// Two things are NOT answers, and both must keep going through the insist
// path, because both are the model failing to produce a plan it does intend:
//
//   - an empty reply (a tool call the server's parser swallowed)
//   - a reply that is talking ABOUT planning: "here are the steps", "I will
//     start by", a numbered list of things to do
//
// Deliberately conservative. A wrong "yes" ends a build turn after one call
// with no work done, which is far worse than a wrong "no" — that merely costs
// the extra ask the gate already made twice.
func answersWithoutAPlan(content string) bool {
	s := strings.TrimSpace(content)
	if s == "" {
		return false
	}
	// Long enough to be a plan in prose; let the translator have it.
	if len(s) > 900 {
		return false
	}
	low := strings.ToLower(s)

	// Plan-shaped language: the model is trying to plan, just not with the
	// tool. planFromText already had its chance; the insist path is next.
	for _, marker := range []string{
		"step 1", "step 1:", "steps:", "here is the plan", "here's the plan",
		"the plan is", "i will start", "i'll start", "first, i", "first i will",
		"plan:", "<commit_plan", "<steps", "<step ",
	} {
		if strings.Contains(low, marker) {
			return false
		}
	}
	// A numbered list of more than one item reads as a plan.
	if len(reNumberedItem.FindAllString(s, 3)) > 1 {
		return false
	}
	// Refusing to plan is not an answer either — it is the gate's own question
	// coming back, and it must not become the user's reply.
	for _, marker := range []string{
		"no plan to commit", "nothing to plan", "no plan is needed",
		"does not need a plan", "doesn't need a plan", "no build",
	} {
		if strings.Contains(low, marker) {
			return false
		}
	}
	return true
}

// reNumberedItem matches "1." / "2)" at the start of a line.
var reNumberedItem = regexp.MustCompile(`(?m)^\s*\d+[.)]\s+\S`)
