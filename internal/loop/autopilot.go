package loop

import (
	"context"
	"encoding/json"
	"errors"
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

	// Verify is the check that proves this step done. Legacy plans without a
	// valid check remain unverified; file existence is not completion evidence.
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

func (l *Loop) RunAutopilot(ctx context.Context, sessionID string) (result *Result, runErr error) {
	ctx, _, finish, err := l.beginRun(ctx, sessionID, "autopilot", "")
	if err != nil {
		return nil, err
	}
	defer func() { finish(result, runErr) }()
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
func (l *Loop) runPlanFrom(ctx context.Context, sessionID string, plan *Plan, sup *Supervisor, start int, single bool, steer string) (result *Result, runErr error) {
	ctx, h, finish, err := l.beginRun(ctx, sessionID, "autopilot", steer)
	if err != nil {
		return nil, err
	}
	defer func() { finish(result, runErr) }()
	wr := h.writer
	reg, notes, err := l.prepareRunRegistry(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	h.registry = reg
	h.skillNotes = notes
	systemPrompt := basePrompt + l.envBlock(sessionID) + "\n\n" + ReminderGuidance + "\n\n" + ModeByName("autopilot").Module
	for _, note := range notes {
		systemPrompt += "\n\n" + note
	}
	original := taskBrief(l.path(sessionID))
	if original != "" && original != h.brief {
		h.brief = original + "\nCurrent instruction: " + h.brief
	}
	var pulls []memory.Pull
	if l.recall != nil {
		pulls = l.recall.TurnStart(ctx, sessionID, plan.Title, nil)
	}
	first := start
	scope, selected := ctx.Value(selectionKey{}).(stepSelection)
	if selected {
		if _, err := wr.Append(episodic.Note, map[string]any{"kind": "selected_scope", "steps": scope, "text": "Server-owned selection; no unselected execution or verification is authorized."}); err != nil {
			return nil, err
		}
	}
	handback := false
	attempts := 0
	for ; attempts < 3*len(plan.Steps)+4; attempts++ {
		if err := h.boundary(ctx); err != nil {
			return &Result{StopReason: "cancelled"}, err
		}
		idx := first
		first = -1
		if idx < 0 {
			if selected {
				idx = scope.next(sup)
			} else {
				idx = sup.Next()
			}
		}
		if idx < 0 {
			handback = !sup.Done()
			if selected {
				handback = !scope.done(sup)
			}
			break
		}
		st := &sup.Steps[idx]
		if selected {
			// Revisions may invalidate dependencies after admission. Never
			// silently widen the selection to repair that changed foundation.
			for j := 0; j < idx; j++ {
				if sup.Steps[j].Status != "passed" {
					st.Reason = fmt.Sprintf("Selected execution paused: step %d needs unfinished step %d first.", idx+1, j+1)
					handback = true
					break
				}
			}
			if handback {
				break
			}
		}
		if err := plan.Steps[idx].Verify.Validate(); err != nil {
			st.Status = "blocked"
			st.Reason = "Plan needs a valid check before execution: " + err.Error()
			st.Verdict = &Verdict{Check: "unverified", Evidence: st.Reason}
			if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
				return nil, err
			}
			handback = true
			break
		}
		h.mu.Lock()
		h.state.Step = idx
		h.mu.Unlock()
		st.Status = "running"
		if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
			return nil, err
		}
		summary, stepErr := l.runStep(ctx, wr, sessionID, systemPrompt, ModeByName("autopilot"), plan, idx, pulls,
			StepPrompt{Index: idx, Rev: st.Rev, Step: plan.Steps[idx], Verify: plan.Steps[idx].Verify,
				Context: stepRunContext(sup, idx, steer) + "\n" + st.Reason})
		if ctx.Err() != nil || h.killed.Load() {
			st.Status = "pending"
			_ = l.saveSupervisor(wr, sessionID, sup)
			return &Result{StopReason: "cancelled", Reply: "Cancelled; completed files are retained."}, ctx.Err()
		}
		var requested *revisionRequest
		needs := -1
		if errors.As(stepErr, &requested) {
			needs = requested.target
			summary = requested.reason
		}
		var verdict Verdict
		if needs >= 0 {
			verdict = Verdict{Check: "revision requested", Evidence: summary}
		} else {
			st.Status = "verifying"
			if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
				return nil, err
			}
			if err := h.boundary(ctx); err != nil {
				return nil, err
			}
			verdict = l.verifyStep(ctx, wr, sessionID, idx, plan.Steps[idx].Verify)
			if stepErr != nil {
				verdict.Evidence = "Execution stopped: " + stepErr.Error() + "\n" + verdict.Evidence
			}
		}
		if ctx.Err() != nil {
			st.Status = "pending"
			_ = l.saveSupervisor(wr, sessionID, sup)
			return nil, ctx.Err()
		}
		dec := sup.Record(idx, verdict, needs)
		if selected && needs >= 0 && !scope.includes(needs) {
			// Record has invalidated the target and downstream evidence. Leave
			// that truth persisted, but require a new explicit scope to fix it.
			dec.HandBack = true
			st.Reason = fmt.Sprintf("Step %d requires revision of unselected step %d. Select the prerequisite or run the whole plan. %s", idx+1, needs+1, summary)
			verdict.Evidence = st.Reason
			st.Verdict.Evidence = st.Reason
		}

		if needs >= 0 && dec.Action == "revise" {
			sup.Steps[needs].Reason = summary
		}
		if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
			return nil, err
		}
		_, planID, _ := LatestPlan(l.path(sessionID))
		cpStatus := statusFor(sup.Steps[idx].Status)
		if needs >= 0 {
			cpStatus = "pending"
		}
		if _, err := wr.Append(episodic.Checkpoint, map[string]any{"plan_event_id": planID, "index": idx, "step": st.Title, "status": cpStatus, "rev": st.Rev, "check": verdict.Check, "evidence": verdict.Evidence, "decision": dec.Action, "projection_only": true}); err != nil {
			return nil, err
		}
		// Reverify is emitted only after the corrected target has passed.
		for _, d := range dec.Reverify {
			if selected && !scope.includes(d) {
				continue // remains needs_reverify until a subsequent authorized run
			}
			if err := h.boundary(ctx); err != nil {
				return nil, err
			}
			rv := l.verifyStep(ctx, wr, sessionID, d, plan.Steps[d].Verify)
			sup.Steps[d].Verdict = &rv
			if rv.Pass {
				sup.Steps[d].Status = "passed"
			} else {
				sup.ReverifyFailed(d, rv)
			}
			remaining := sup.reverify[:0]
			for _, x := range sup.reverify {
				if x != d {
					remaining = append(remaining, x)
				}
			}
			sup.reverify = remaining
			if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
				return nil, err
			}
		}
		if len(sup.reverify) == 0 {
			sup.revisionTarget = -1
		}
		if dec.HandBack {
			handback = true
			break
		}
		if single {
			break
		}
	}
	if attempts >= 3*len(plan.Steps)+4 {
		handback = true
	}
	if err := l.saveSupervisor(wr, sessionID, sup); err != nil {
		return nil, err
	}
	results := make([]StepResult, len(plan.Steps))
	for i, st := range sup.Steps {
		results[i] = StepResult{Step: st.Title, Status: statusFor(st.Status), Summary: st.Reason}
		if st.Verdict != nil {
			results[i].Summary = summaryFor(*st.Verdict, "")
		}
	}
	report := renderReport(plan, results, handback)
	if selected {
		if scope.done(sup) {
			report = "Selected steps passed. Unselected steps were not executed.\n\n" + report
		} else {
			report = "Selected execution stopped before every selected step passed.\n\n" + report
		}
	}
	if _, err := wr.Append(episodic.MsgAssistant, map[string]any{"text": report}); err != nil {
		return nil, err
	}
	if _, err := wr.Append(episodic.TurnClose, map[string]any{"autopilot": true, "handback": handback, "plan_complete": sup.Done(), "selected_steps": scope}); err != nil {
		return nil, err
	}
	l.runBoundary(sessionID)
	stop := StopFinalAnswer
	if handback {
		stop = "plan_blocked"
	}
	h.mu.Lock()
	calls := h.state.Calls
	h.mu.Unlock()
	return &Result{Reply: report, Iterations: calls, StopReason: stop, Pulls: len(pulls)}, nil
}

type revisionRequest struct {
	target int
	reason string
}

func (r *revisionRequest) Error() string { return r.reason }

func (l *Loop) runStep(ctx context.Context, wr *episodic.Writer, sid, systemPrompt string, mode Mode, p *Plan, idx int, pulls []memory.Pull, sp StepPrompt) (string, error) {
	h := handleOf(ctx)
	reg := h.registry
	goal := sp.Text()
	items := []window.Item{
		{Msg: llm.Message{Role: "system", Content: systemPrompt}, Kind: "system"},
		{Msg: llm.Message{Role: "user", Content: "Task constraints (do only the active step):\n" + h.brief + "\nPlan: " + p.Title + "\n" + goal}, Kind: "pinned"},
	}
	if text := wrapReminder(memory.FormatPulls(pulls)); text != "" {
		items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: text}, Kind: "pulls"})
	}
	g := newTurnGuardBudget(0, maxStepTime)
	work := newWorkTracker(h.state.Workspace)
	extFP, _ := work.fingerprint()
	level := h.thinkingFor(mode.Name, false)
	seenSteer := map[string]bool{}
	if events, err := episodic.Replay(l.path(sid)); err == nil {
		for _, ev := range events {
			seenSteer[ev.ID] = true
		}
	}
	for i := 1; ; i++ {
		wasPaused := h.paused.Load()
		if err := h.boundary(ctx); err != nil {
			return "", err
		}
		if wasPaused {
			g.progress()
		}
		if events, err := episodic.Replay(l.path(sid)); err == nil {
			for _, ev := range events {
				if seenSteer[ev.ID] || ev.Type != episodic.MsgUser {
					continue
				}
				seenSteer[ev.ID] = true
				var msg struct{ Text string }
				if json.Unmarshal(ev.Payload, &msg) == nil {
					items = append(items, window.Item{Msg: llm.Message{Role: "user", Content: msg.Text}, Kind: "pinned", EvtID: ev.ID})
				}
			}
		}
		fp, _ := work.fingerprint()
		g.observeWorkspace(fp)
		if i > g.maxIter+g.iterExts*g.maxIter && fp != extFP && g.extendIter() {
			extFP = fp
			wr.Append(episodic.Note, map[string]string{"kind": "iteration_checkpoint", "text": "Workspace changed; granting one bounded additional attempt budget."})
		}
		if g.tokensExhausted() && g.extendTokens() {
			wr.Append(episodic.Note, map[string]string{"kind": "token_checkpoint", "text": "Continuing with an additional bounded token slice."})
		}
		if _, detail, tripped := g.preThink(i); tripped {
			return "", fmt.Errorf("guard: %s", detail)
		}
		msgs, _ := l.compress(ctx, items)
		specs := reg.Specs(mode.Name)
		filtered := specs[:0:0]
		for _, s := range specs {
			if s.Function.Name != "commit_plan" {
				filtered = append(filtered, s)
			}
		}
		specs = filtered
		if idx > 0 {
			specs = append(specs, llm.ToolSpec{Type: "function", Function: llm.FunctionSpec{Name: "request_revision", Description: "Stop this step and request a correction to an earlier step. Zero-based target; explain exactly what must change.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"target": map[string]any{"type": "integer", "minimum": 0, "maximum": idx - 1}, "reason": map[string]any{"type": "string"}}, "required": []string{"target", "reason"}}}})
		}
		reply, usage, err := l.completeWithRetry(llm.WithThinking(ctx, level), wr, msgs, specs, "", mode.ProseCap)
		if errors.Is(err, errControl) {
			h.steered.Store(false)
			i--
			g.progress()
			continue
		}
		g.addTokens(usage.AnswerTokens())
		if err != nil {
			return "", err
		}
		if _, err := wr.Append(episodic.MsgAssistant, assistantPayload(reply, usage)); err != nil {
			return "", err
		}
		if reply.Truncated() && level != llm.ThinkingOff {
			level = llm.StepDown(level)
			wr.Append(episodic.Note, map[string]string{"kind": "self_correct", "text": "Reasoning budget exhausted; next call uses " + level})
			continue
		}
		if len(reply.ToolCalls) == 0 {
			if strings.TrimSpace(reply.Content) == "" {
				return "", fmt.Errorf("model returned no answer or tool call")
			}
			return reply.Content, nil
		}
		items = append(items, window.Item{Msg: llm.Message{Role: "assistant", Content: reply.Content, ToolCalls: reply.ToolCalls}, Kind: "assistant"})
		for _, tc := range reply.ToolCalls {
			if err := h.boundary(ctx); err != nil {
				return "", err
			}
			if tc.Function.Name == "request_revision" {
				var r struct {
					Target *int
					Reason string
				}
				if json.Unmarshal([]byte(tc.Function.Arguments), &r) == nil && r.Target != nil && *r.Target >= 0 && *r.Target < idx && strings.TrimSpace(r.Reason) != "" {
					wr.Append(episodic.ToolCall, map[string]any{"id": tc.ID, "name": tc.Function.Name, "args": json.RawMessage(tc.Function.Arguments)})
					wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": true, "output": "revision requested: " + r.Reason})
					return "", &revisionRequest{*r.Target, r.Reason}
				}
			}
			out, execErr, eventID := l.executeCall(ctx, wr, reg, specs, mode.Name, tc)
			items = append(items, window.Item{Msg: llm.Message{Role: "tool", ToolCallID: tc.ID, Content: out}, Kind: "tool", EvtID: eventID})
			fp, _ = work.fingerprint()
			g.observeWorkspace(fp)
			if execErr != nil {
				if detail, tripped := g.toolError(tc.Function.Name, out); tripped {
					return "", fmt.Errorf("%s: %s", detail, out)
				}
			} else {
				g.toolOK(tc.Function.Name)
				if !g.seenBefore(tc.Function.Name, json.RawMessage(tc.Function.Arguments), out) {
					g.progress()
				}
			}
			if detail, tripped := g.repeatedResult(tc.Function.Name, json.RawMessage(tc.Function.Arguments), out); tripped {
				return "", fmt.Errorf("guard: %s", detail)
			}
		}
	}
}

func renderReport(plan *Plan, results []StepResult, handback bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Autopilot report — %s\n\n", plan.Title)
	done, failed, skipped := 0, 0, 0
	for i, r := range results {
		icon := "✓"
		switch r.Status {
		case "failed", "blocked":
			icon = "✗"
			failed++
		case "skipped", "pending", "needs_reverify", "unverified":
			icon = "·"
			skipped++
		case "done":
			done++
		default:
			icon = "·"
		}
		fmt.Fprintf(&sb, "%s %d. %s — %s\n   %s\n", icon, i+1, r.Step, r.Status, strings.TrimSpace(r.Summary))
	}
	fmt.Fprintf(&sb, "\n%d done · %d failed · %d skipped", done, failed, skipped)
	if handback {
		sb.WriteString("\nWork is paused for a decision. Inspect the failed check or budget stop before retrying or revising the plan.")
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
	// The last attempt's verdict, whether this is a retry or a revision. A
	// retry used to start blind: the harness ran the check at the cap, saw
	// "Uncaught TypeError: Cannot read properties of undefined (reading 'x')"
	// 2,128 times in the console, recorded it in the checkpoint — and the
	// next attempt's fresh window never heard of it. The model re-read its
	// file, edited blind, and ran out again (NFQ step 4, 2026-09-05).
	if st := sup.Steps[idx]; st.Verdict != nil && !st.Verdict.Pass {
		parts = append(parts, "Your previous attempt at this step FAILED its check: "+st.Verdict.Check+
			"\nWhat was observed: "+clipEvidence(st.Verdict.Evidence)+
			"\nFix that before anything else, then run the check yourself before you stop.")
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
