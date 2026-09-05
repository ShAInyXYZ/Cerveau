package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/memory"
	"cerveau/internal/plan"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

type PlanStep struct {
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	Files  []string `json:"files"`
	Risk   string   `json:"risk"`

	// Verify is the check that proves this step done. Absent on plans committed
	// through the markdown path, and on every plan written before step-wise
	// execution existed — those fall back to disk reconciliation.
	Verify *plan.Verify `json:"verify,omitempty"`
}

type Plan struct {
	Title          string     `json:"title"`
	Steps          []PlanStep `json:"steps"`
	AutonomyBudget string     `json:"autonomy_budget"`
}

// AsGuidance renders the plan as a guidance block for the autopilot system prompt.
// It is intent, not a rigid script — the agent adapts as needed.
func (p *Plan) AsGuidance() string {
	var b strings.Builder
	fmt.Fprintf(&b, "COMMITTED PLAN (guidance): %s\n", p.Title)
	for i, s := range p.Steps {
		fmt.Fprintf(&b, "%d. %s", i+1, s.Title)
		if s.Detail != "" {
			fmt.Fprintf(&b, " — %s", s.Detail)
		}
		if len(s.Files) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(s.Files, ", "))
		}
		b.WriteByte('\n')
	}
	b.WriteString("Follow this plan's intent; re-plan on the fly if a step proves wrong or blocked.")
	return b.String()
}

func LatestPlan(eventsPath string) (*Plan, string, error) {
	events, err := episodic.Replay(eventsPath)
	if err != nil {
		return nil, "", err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != episodic.Plan {
			continue
		}
		var p Plan
		if err := json.Unmarshal(events[i].Payload, &p); err != nil {
			return nil, "", fmt.Errorf("bad plan payload: %w", err)
		}
		return &p, events[i].ID, nil
	}
	return nil, "", fmt.Errorf("no committed plan in this session — agree one in Discussion and call commit_plan")
}

type StepResult struct {
	Step    string `json:"step"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func (l *Loop) RunAutopilot(ctx context.Context, sessionID string) (*Result, error) {
	plan, planEvt, err := LatestPlan(l.path(sessionID))
	if err != nil {
		return nil, err
	}
	wr, err := l.open(sessionID)
	if err != nil {
		return nil, err
	}
	wr.Append(episodic.Note, map[string]string{"text": fmt.Sprintf("autopilot started on %s (%d steps)", planEvt, len(plan.Steps))})

	// The supervisor owns the cursor. Steps are no longer a for-range over the
	// plan: a step that fails is retried, a step that needs earlier work
	// reopens it as a revision, and only a PASSED check advances. Which step
	// runs next is its decision, not the loop counter's.
	sup, err := l.restoreSupervisor(sessionID, plan)
	if err != nil {
		return nil, err
	}
	return l.runPlanFrom(ctx, sessionID, plan, sup, sup.Next(), false, "")
}

// runPlanFrom is the one execution path. Autopilot walks the whole plan;
// RunStep sets single to true and stops after one step. Sharing the body is
// deliberate: a step run by a button must obey exactly the same rules — its own
// prompt, its own check, a checkpoint carrying the verdict — as a step run by
// autopilot, or the two surfaces drift apart again.
func (l *Loop) runPlanFrom(ctx context.Context, sessionID string, plan *Plan, sup *Supervisor, start int, single bool, steer string) (*Result, error) {
	ctx = tools.WithSession(ctx, sessionID) // see Run
	wr, err := l.open(sessionID)
	if err != nil {
		return nil, err
	}
	runCtx, rootCancel := context.WithCancel(ctx)
	h := &runHandle{rootCancel: rootCancel}
	defer l.runs.register(sessionID, h)()
	defer rootCancel()

	mode := ModeByName("autopilot")
	systemPrompt := basePrompt + l.envBlock(sessionID) + "\n\n" + ReminderGuidance + "\n\n" + mode.Module

	var pulls []memory.Pull
	if l.recall != nil {
		pulls = l.recall.TurnStart(runCtx, sessionID, plan.Title, nil)
	}

	sources := planningSources(l.path(sessionID))

	results := make([]StepResult, len(plan.Steps))
	for i, st := range plan.Steps {
		results[i] = StepResult{Step: st.Title, Status: sup.Steps[i].Status}
		if results[i].Status == "passed" {
			results[i].Status = "done"
			if v := sup.Steps[i].Verdict; v != nil {
				results[i].Summary = v.Evidence
			}
		}
		_ = st
	}
	handback := false
	first := start

	// A hard ceiling on runs, not on steps: retries and revisions are extra
	// runs by design, and without a cap a plan could ask for them forever.
	maxRuns := 3*len(plan.Steps) + 4

	for runs := 0; runs < maxRuns; runs++ {
		idx := first
		first = -1
		if idx < 0 {
			idx = sup.Next()
		}
		if idx < 0 {
			break // done, or blocked
		}
		if h.killed.Load() {
			results[idx].Status = "skipped"
			results[idx].Summary = "killed by user"
			handback = true
			break
		}

		state := sup.Steps[idx]
		results[idx].Status = "running"

		summary, stepErr := l.runStep(runCtx, wr, sessionID, systemPrompt, mode, plan, idx, pulls,
			StepPrompt{
				Index:   idx,
				Rev:     state.Rev,
				Step:    plan.Steps[idx],
				Verify:  plan.Steps[idx].Verify,
				Context: stepRunContext(sup, idx, steer),
				Sources: sources,
			})

		// The step's own check decides — even when the run did not end
		// cleanly. The final car run wrote js/input.js, then kept reading
		// instead of stopping, hit the iteration cap, and was marked failed
		// with its check never run. The file satisfied the check; the exit
		// code decided instead of the observation. A user kill is the one
		// thing that skips the check: nothing was allowed to finish.
		runFailed := stepErr != nil
		if runFailed && h.killed.Load() {
			results[idx].Status = "skipped"
			results[idx].Summary = "killed by user"
			handback = true
			break
		}
		if runFailed {
			summary = "run did not finish (" + stepErr.Error() + ")"
		}

		// A plan committed through the markdown path (or before verifies
		// existed) declares none. Then the model's summary is all there is:
		// accept it from a run that finished, say "unverified" plainly, and
		// never accept it from one that did not.
		verdict := Verdict{Pass: !runFailed, Check: "no check declared", Evidence: "unverified: " + summary}
		if v := plan.Steps[idx].Verify; v != nil {
			verdict = RunVerify(runCtx, l.registryFor(sessionID), l.workspace(sessionID), v)
			if runFailed {
				verdict.Evidence = summary + " — " + verdict.Evidence
			}
		}

		needs := needsStepFrom(summary, idx)
		dec := sup.Record(idx, verdict, needs)

		results[idx].Status = statusFor(sup.Steps[idx].Status)
		results[idx].Summary = summaryFor(verdict, summary)
		wr.Append(episodic.Checkpoint, map[string]any{
			"step": plan.Steps[idx].Title, "index": idx, "rev": state.Rev,
			"status": results[idx].Status, "summary": results[idx].Summary,
			"check": verdict.Check, "evidence": clipEvidence(verdict.Evidence),
			"decision": dec.Action, "why": dec.Reasoning})

		// A revision can invalidate a later step whose check was observed
		// against the old file. Re-run those checks — cheap, because it is the
		// declared check and not another run.
		for _, d := range dec.Reverify {
			if plan.Steps[d].Verify == nil {
				continue
			}
			rv := RunVerify(runCtx, l.registryFor(sessionID), l.workspace(sessionID), plan.Steps[d].Verify)
			if !rv.Pass {
				sup.ReverifyFailed(d, rv)
				results[d].Status = "pending"
				results[d].Summary = "re-check failed after step " + fmt.Sprint(dec.Step+1) + " changed: " + rv.Check
				wr.Append(episodic.Checkpoint, map[string]any{
					"step": plan.Steps[d].Title, "index": d, "status": "pending",
					"summary": results[d].Summary, "check": rv.Check, "evidence": clipEvidence(rv.Evidence)})
			}
		}

		if dec.HandBack {
			handback = true
			wr.Append(episodic.Note, map[string]string{"kind": "step_handback", "text": dec.Reasoning})
			break
		}
		// One step, by request: a button that says "run step 3" runs step 3
		// and stops, so the user sees the verdict before anything else moves.
		if single {
			break
		}
	}

	// Anything the cursor never reached.
	for i := range results {
		if results[i].Status == "pending" || results[i].Status == "running" {
			results[i].Status = "skipped"
			if results[i].Summary == "" {
				results[i].Summary = "not reached"
			}
		}
	}

	report := renderReport(plan, results, handback)
	wr.Append(episodic.MsgAssistant, map[string]any{"text": report})
	wr.Append(episodic.TurnClose, map[string]any{"autopilot": true, "handback": handback})
	l.runBoundary(sessionID)
	stopReason := StopFinalAnswer
	if handback {
		stopReason = "plan_drift_handback"
	}
	return &Result{Reply: report, Iterations: len(plan.Steps), StopReason: stopReason, Pulls: len(pulls)}, nil
}

func (l *Loop) runStep(ctx context.Context, wr *episodic.Writer, sessionID, systemPrompt string, mode Mode, plan *Plan, idx int, pulls []memory.Pull, sp StepPrompt) (string, error) {
	// The prompt for THIS step, carrying its check verbatim. The user's
	// original prompt is not here: it was used once, to plan. Re-injecting it
	// into every step is what let a run wander across the whole task and made
	// the plan decoration.
	stepGoal := sp.Text()

	// The SESSION's registry, jailed to the session's workspace — not the
	// global one. Every tool captures its jail root at construction, so the
	// global registry writes into the global workspace whatever the session
	// says. The first hand-off run wrote js/config.js twice, successfully,
	// somewhere else, and its own check — which reads the session workspace
	// — found no such file (2026-09-04). The chat path had this fix already;
	// the step runner did not.
	stepReg := l.registryFor(sessionID)

	items := []window.Item{{Msg: llm.Message{Role: "system", Content: systemPrompt}, Kind: "system"}}
	// see loop.go: the template allows exactly one system message, at index 0
	if text := wrapReminder(memory.FormatPulls(pulls)); text != "" {
		items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "pulls"})
	}
	planPayload := fmt.Sprintf("Plan: %s (step %d/%d in progress)", plan.Title, idx+1, len(plan.Steps))
	items = append(items,
		window.Item{Msg: llm.Message{Role: "user", Content: planPayload}, Kind: "user"},
		window.Item{Msg: llm.Message{Role: "user", Content: stepGoal}, Kind: "user"},
	)
	// What the plan was MADE from. A step starts with a fresh window, so
	// without this the model has never seen the file it was told to split:
	// it re-reads all 435 lines, which alone costs the iterations it had,
	// and never reaches a write (2026-09-04, improve-overrun — no writes
	// at all in two attempts). Demoted to a tool-result item so the window
	// manager treats it like any other read.
	if sp.Sources != "" {
		items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: sp.Sources}, Kind: "tool"})
	}

	g := newTurnGuard(0)
	// The guard needs to know when the workspace moves, or a re-check after
	// an edit that returns the same answer reads as a loop (see guard.go).
	stepWS := ""
	if l.workspace != nil {
		stepWS = l.workspace(sessionID)
	}
	work := newWorkTracker(stepWS) // an empty root measures nothing and never blocks
	// The session's THINK level. Steps used to inherit the client default —
	// off — whatever the knob said: at xhigh the model debugged a keypress
	// that did not register with 154-token replies and zero reasoning, and
	// re-ran the same probe six times (2026-09-05, NFQ). mode "plan" still
	// means plan-only; "autopilot" and "always" now reach the steps.
	level := l.thinkingFor(mode.Name)
	lastText := ""
	for i := 1; i <= maxStepIterations; i++ {
		fp, _ := work.fingerprint()
		g.observeWorkspace(fp)
		// Same checkpoint-instead-of-death as the chat loop: a step that
		// builds several files legitimately spends more than one budget slice.
		if g.tokensExhausted() && g.extendTokens() {
			wr.Append(episodic.Note, map[string]string{"kind": "token_checkpoint",
				"text": "token budget checkpoint — budget refreshed; continue the step in progress, do not restart it"})
		}
		if _, detail, tripped := g.preThink(i); tripped {
			return "", fmt.Errorf("guard: %s", detail)
		}
		// Compress through the window manager exactly like a chat turn.
		// Sending raw items let a long step grow past the model's context
		// ("request (33144 tokens) exceeds the available context size") —
		// tool results are demoted to event pointers, then oldest-first
		// trimmed, so the step survives instead of dying at the ceiling.
		msgs, rep := l.compress(ctx, items)
		if rep.Zone == window.ZoneRed {
			wr.Append(episodic.Note, map[string]string{"kind": "window",
				"text": fmt.Sprintf("step window compressed: %d demoted, %d trimmed (%d tok)", rep.Demoted, rep.Trimmed, rep.Tokens)})
		}
		reply, usage, err := l.completeWithRetry(llm.WithThinking(ctx, level), wr, msgs, stepReg.Specs(mode.Name), "", mode.ProseCap)
		g.addTokens(usage.AnswerTokens())
		if err != nil {
			return "", err
		}
		// Reasoning that overran its budget leaves no answer to act on. Same
		// graded fallback as the plan gate: step down and try the call again.
		if reply.Truncated() && level != llm.ThinkingOff {
			next := llm.StepDown(level)
			wr.Append(episodic.Note, map[string]string{"kind": "self_correct",
				"text": fmt.Sprintf("step thinking at %s ran past its budget (%d reasoning tokens) — retrying at %s", level, usage.ReasoningTokens, next)})
			level = next
			continue
		}
		if len(reply.ToolCalls) == 0 {
			return reply.Content, nil
		}
		wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage))
		items = append(items, window.Item{Msg: llm.Message{Role: "assistant", Content: reply.Content, ToolCalls: reply.ToolCalls}, Kind: "assistant"})
		for _, tc := range reply.ToolCalls {
			args := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage(`{}`)
			}
			wr.Append(episodic.ToolCall, map[string]any{"id": tc.ID, "name": tc.Function.Name, "args": json.RawMessage(tc.Function.Arguments)})
			out, execErr := stepReg.ExecuteMode(ctx, tc.Function.Name, args, mode.Name)
			if execErr != nil {
				// keep the command's own output — it explains the failure
				if out != "" {
					out = out + "\n" + execErr.Error()
				} else {
					out = execErr.Error()
				}
				if detail, tripped := g.toolError(tc.Function.Name, out); tripped {
					wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": false, "output": out})
					return "", fmt.Errorf("%s, last: %s", detail, out)
				}
			} else {
				g.toolOK(tc.Function.Name)
			}
			g.progress() // a tool returned — the turn is moving, not stalled
			wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": execErr == nil, "output": out})
			items = append(items, window.Item{Msg: llm.Message{Role: "tool", ToolCallID: tc.ID, Content: out}, Kind: "tool"})
			// loop detection on the RESULT (same call + same output = stuck)
			if detail, tripped := g.repeatedResult(tc.Function.Name, args, out); tripped {
				return "", fmt.Errorf("guard: %s", detail)
			}
		}
		if reply.Content != "" {
			lastText = reply.Content
		}
	}
	if lastText != "" {
		return lastText, nil
	}
	return "", fmt.Errorf("step exceeded 4 iterations")
}

func renderReport(plan *Plan, results []StepResult, handback bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Autopilot report — %s\n\n", plan.Title)
	done, failed, skipped := 0, 0, 0
	for i, r := range results {
		icon := "✓"
		switch r.Status {
		case "failed":
			icon = "✗"
			failed++
		case "skipped":
			icon = "·"
			skipped++
		default:
			done++
		}
		fmt.Fprintf(&sb, "%s %d. %s — %s\n   %s\n", icon, i+1, r.Step, r.Status, strings.TrimSpace(r.Summary))
	}
	fmt.Fprintf(&sb, "\n%d done · %d failed · %d skipped", done, failed, skipped)
	if handback {
		sb.WriteString("\nHanded back early: a step failed under a low autonomy budget. Adjust the plan or re-run.")
	}
	return sb.String()
}

// outOfPlanNote flags a write that creates a file the committed plan never
// declared. Architecture drift (the model inventing extra files) was invisible
// before — a one-file build silently became four files and only failed at
// runtime. The note doesn't block the write (plans adapt); it makes the drift
// something the model and the user can SEE and correct.
func outOfPlanNote(p *Plan, tool string, args []byte) string {
	if p == nil || tool != "write" {
		return ""
	}
	declared := map[string]bool{}
	for _, s := range p.Steps {
		for _, f := range s.Files {
			declared[f] = true
		}
	}
	if len(declared) == 0 {
		return "" // a plan without file declarations constrains nothing
	}
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &a) != nil || a.Path == "" || declared[a.Path] {
		return ""
	}
	files := make([]string, 0, len(declared))
	for f := range declared {
		files = append(files, f)
	}
	sort.Strings(files)
	return fmt.Sprintf("note: %s is not in the committed plan (declared files: %s). "+
		"If this extra file is intentional, continue; otherwise keep to the planned layout.",
		a.Path, strings.Join(files, ", "))
}

// autoCommitPlanFile is the harness-side answer to the model's stubbornest
// habit: writing its plan to a .md file instead of calling commit_plan. If a
// write is plan-shaped (plan-named markdown with parseable steps) and it
// parses, the harness commits the structured plan itself — the file still
// lands on disk, AND the plan card + Planner see a real plan event. The
// translation is disclosed in the returned note. Prompt pleading demonstrably
// failed here twice; translation always runs.
func autoCommitPlanFile(wr *episodic.Writer, tool string, args []byte) (*Plan, string) {
	if tool != "write" {
		return nil, ""
	}
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if json.Unmarshal(args, &a) != nil || !tools.PlanLike(a.Path, a.Content) {
		return nil, ""
	}
	title, parsed := tools.ParsePlanMarkdown(a.Content)
	if title == "" {
		title = a.Path
	}
	steps := make([]PlanStep, 0, len(parsed))
	for _, p := range parsed {
		steps = append(steps, PlanStep{Title: p.Title, Detail: p.Detail, Files: p.Files, Risk: p.Risk})
	}
	plan := &Plan{Title: title, Steps: steps, AutonomyBudget: "low"}
	if _, err := wr.Append(episodic.Plan, map[string]any{
		"title": plan.Title, "steps": plan.Steps, "autonomy_budget": plan.AutonomyBudget,
	}); err != nil {
		return nil, ""
	}
	return plan, fmt.Sprintf("note: %s looked like a plan, so it was ALSO committed as a structured plan (%d steps) — it now appears in the plan card. Next time call commit_plan directly.", a.Path, len(steps))
}

// planFromText translates a plan the model wrote as XML-style TEXT into a
// committed plan. With thinking on, Qwen3.8 twice answered the planning
// call with
//
//	<commit_plan><steps><step name="skeleton" files="a.html" description="…">…</step>…
//
// — a tool call in its head, in a shape the tool parser does not know. Same
// rule as plan-shaped .md writes: translate, don't plead. Returns nil when
// the text has no such steps.
func planFromText(wr *episodic.Writer, text string) (*Plan, string) {
	if !strings.Contains(text, "<step") {
		return nil, ""
	}
	var steps []PlanStep
	for _, m := range xmlSteps(text) {
		attrs, body := m[0], strings.TrimSpace(m[1])
		st := PlanStep{Title: xmlAttr(attrs, "name", "title"), Detail: xmlAttr(attrs, "description", "detail")}
		if st.Detail == "" {
			st.Detail = body
		}
		if st.Title == "" {
			st.Title = firstLine(body)
		}
		for _, f := range strings.FieldsFunc(xmlAttr(attrs, "files", "file"), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
			st.Files = append(st.Files, strings.TrimSpace(f))
		}
		if st.Title != "" {
			steps = append(steps, st)
		}
	}
	if len(steps) == 0 {
		return nil, ""
	}
	title := xmlAttr(text, "title", "name")
	if title == "" || len(title) > 80 {
		title = "Plan"
	}
	plan := &Plan{Title: title, Steps: steps, AutonomyBudget: "low"}
	if _, err := wr.Append(episodic.Plan, map[string]any{
		"title": plan.Title, "steps": plan.Steps, "autonomy_budget": plan.AutonomyBudget,
	}); err != nil {
		return nil, ""
	}
	return plan, fmt.Sprintf("your plan was written as text, not as a commit_plan tool call — it was translated and committed anyway (%d steps). Next time call the tool. Start on step 1 now.", len(steps))
}

var (
	// an opening <step …> tag; attribute values may hold '>' inside quotes
	xmlStepOpen = regexp.MustCompile(`(?is)<step\b((?:"[^"]*"|[^>"])*?)(/?)>`)
	xmlStepEnd  = regexp.MustCompile(`(?i)</step>`)
	xmlAttrRe   = regexp.MustCompile(`(?i)\b([a-z_]+)\s*=\s*"([^"]*)"`)
)

// xmlSteps returns (attrs, body) for every step in document order, whether
// written as <step …>body</step> (Crane5) or self-closing <step … /> with a
// description attribute (Crane6). A step without a closing tag ends at the
// next <step or at the end of the text.
func xmlSteps(text string) [][2]string {
	var out [][2]string
	locs := xmlStepOpen.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		attrs := text[loc[2]:loc[3]]
		selfClosing := loc[5] > loc[4] // the "/" group matched
		body := ""
		if !selfClosing {
			end := len(text)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			seg := text[loc[1]:end]
			if m := xmlStepEnd.FindStringIndex(seg); m != nil {
				seg = seg[:m[0]]
			}
			body = seg
		}
		out = append(out, [2]string{attrs, body})
	}
	return out
}

// xmlAttr returns the first of the named attributes present in attrs.
func xmlAttr(attrs string, names ...string) string {
	found := map[string]string{}
	for _, m := range xmlAttrRe.FindAllStringSubmatch(attrs, -1) {
		if _, ok := found[strings.ToLower(m[1])]; !ok {
			found[strings.ToLower(m[1])] = strings.TrimSpace(m[2])
		}
	}
	for _, n := range names {
		if v := found[n]; v != "" {
			return v
		}
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// stepRunContext is what a step's run is told about the ground it stands on:
// the steps already verified, and — for a revision — who asked for it and why.
func stepRunContext(sup *Supervisor, idx int, steer string) string {
	parts := []string{}
	if strings.TrimSpace(steer) != "" {
		parts = append(parts, "The user says: "+strings.TrimSpace(steer))
	}
	if c := StepContext(sup); c != "" {
		parts = append(parts, c)
	}
	if st := sup.Steps[idx]; st.Rev > 0 && st.Verdict != nil {
		parts = append(parts, st.Verdict.Evidence)
	}
	return strings.Join(parts, "\n\n")
}

// reNeedsStep finds a run asking for an EARLIER step to be reopened.
//
// The model asks; the harness never guesses. A run that discovers step 1 should
// have declared a variable it needs says so in its report, and the supervisor
// turns that into a revision run rather than letting the model patch around it
// here — which is how a later step quietly grows a workaround for an earlier
// step's omission.
var reNeedsStep = regexp.MustCompile(`(?i)\bstep\s+(\d+)\s+(?:must|needs to|should|has to)\b`)

// needsStepFrom reports the 0-based index of an earlier step the run asked to
// reopen, or -1. Only EARLIER steps count: a run naming a later step is
// describing what comes next, not a dependency it is blocked on.
func needsStepFrom(summary string, idx int) int {
	m := reNeedsStep.FindStringSubmatch(summary)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 {
		return -1
	}
	if target := n - 1; target < idx {
		return target
	}
	return -1
}

// statusFor maps the supervisor's step state to the report's vocabulary.
func statusFor(s string) string {
	switch s {
	case "passed":
		return "done"
	case "blocked":
		return "failed"
	case "failed":
		return "failed"
	}
	return s
}

// summaryFor prefers what was OBSERVED over what was claimed. The model's own
// sentence is kept as context, never as the verdict.
func summaryFor(v Verdict, modelSummary string) string {
	if v.Pass {
		if v.Check == "no check declared" {
			return modelSummary
		}
		return "verified: " + v.Check
	}
	if v.Evidence != "" {
		return "check failed (" + v.Check + "): " + clipEvidence(v.Evidence)
	}
	return "check failed: " + v.Check
}

func clipEvidence(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:297] + "…"
	}
	return s
}

// maxStepIterations bounds one step's run. It was 4, which a step that must
// read a 435-line file before writing cannot fit. The loop guards — repeat,
// idle, error — are what catch a step that is circling; the cap is only the
// backstop, so it can be generous.
const maxStepIterations = 10

// planningSourcesCap bounds how much read material rides into each step.
const planningSourcesCap = 40000

// planningSources collects what the model read while planning THIS turn: the
// tool results of read / grep / outline_file between the last user message
// and the plan event. Every step then starts knowing the source the plan
// describes, instead of re-reading it under its own budget.
func planningSources(eventsPath string) string {
	events, err := episodic.Replay(eventsPath)
	if err != nil {
		return ""
	}
	start := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.MsgUser {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	var b strings.Builder
	calls := map[string]string{} // tool call id → "name path"
	for _, ev := range events[start:] {
		switch ev.Type {
		case episodic.Plan:
			// reads AFTER the plan are a step's own, not planning
			return finishSources(&b)
		case episodic.ToolCall:
			var c struct {
				ID   string          `json:"id"`
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			}
			if json.Unmarshal(ev.Payload, &c) == nil {
				var a struct {
					Path string `json:"path"`
				}
				json.Unmarshal(c.Args, &a)
				calls[c.ID] = c.Name + " " + a.Path
			}
		case episodic.ToolResult:
			var r struct {
				ID     string `json:"id"`
				OK     bool   `json:"ok"`
				Output string `json:"output"`
			}
			if json.Unmarshal(ev.Payload, &r) != nil || !r.OK {
				continue
			}
			what := calls[r.ID]
			if !(strings.HasPrefix(what, "read ") || strings.HasPrefix(what, "grep ") || strings.HasPrefix(what, "outline_file ")) {
				continue
			}
			if b.Len()+len(r.Output) > planningSourcesCap {
				continue
			}
			if b.Len() == 0 {
				b.WriteString("SOURCE you already read while planning — do not re-read it, build from it:\n")
			}
			fmt.Fprintf(&b, "\n--- %s ---\n%s\n", what, r.Output)
		}
	}
	return finishSources(&b)
}

func finishSources(b *strings.Builder) string {
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}
