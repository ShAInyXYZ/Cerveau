package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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
	results := []StepResult{}
	handback := false
	for i, step := range plan.Steps {
		if h.killed.Load() {
			results = append(results, StepResult{Step: step.Title, Status: "skipped", Summary: "killed by user"})
			handback = true
			continue
		}
		if handback {
			results = append(results, StepResult{Step: step.Title, Status: "skipped", Summary: "handed back earlier"})
			continue
		}
		summary, stepErr := l.runStep(runCtx, wr, sessionID, systemPrompt, mode, plan, i, pulls)
		if stepErr != nil {
			results = append(results, StepResult{Step: step.Title, Status: "failed", Summary: stepErr.Error()})
			wr.Append(episodic.Checkpoint, map[string]string{"step": step.Title, "status": "failed", "detail": stepErr.Error()})
			if plan.AutonomyBudget != "high" {
				handback = true
			}
			continue
		}
		results = append(results, StepResult{Step: step.Title, Status: "done", Summary: summary})
		wr.Append(episodic.Checkpoint, map[string]string{"step": step.Title, "status": "done", "summary": summary})
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

func (l *Loop) runStep(ctx context.Context, wr *episodic.Writer, sessionID, systemPrompt string, mode Mode, plan *Plan, idx int, pulls []memory.Pull) (string, error) {
	step := plan.Steps[idx]
	stepGoal := fmt.Sprintf("Execute step %d of %d: %s", idx+1, len(plan.Steps), step.Title)
	if step.Detail != "" {
		stepGoal += "\nDetail: " + step.Detail
	}
	if len(step.Files) > 0 {
		stepGoal += "\nFiles: " + strings.Join(step.Files, ", ")
	}

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

	g := newTurnGuard(0)
	lastText := ""
	for i := 1; i <= 4; i++ {
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
		reply, usage, err := l.completeWithRetry(ctx, wr, msgs, l.registry().Specs(mode.Name), "", mode.ProseCap)
		g.addTokens(usage.AnswerTokens())
		if err != nil {
			return "", err
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
			out, execErr := l.registry().ExecuteMode(ctx, tc.Function.Name, args, mode.Name)
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
