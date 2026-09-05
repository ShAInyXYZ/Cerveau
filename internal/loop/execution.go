package loop

import (
	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/plan"
	"cerveau/internal/tools"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var errControl = errors.New("model call interrupted by user control")

var buildIntent = regexp.MustCompile(`(?i)\b(build|create|implement|fix|change|refactor|add|make|update|write|remove|improve|develop)\b`)

func requiresPlan(text string) bool { return buildIntent.MatchString(text) }

func taskBrief(path string) string {
	events, err := episodic.Replay(path)
	if err != nil {
		return ""
	}
	end := len(events)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.Plan {
			end = i
			break
		}
	}
	for i := end - 1; i >= 0; i-- {
		if events[i].Type == episodic.MsgUser {
			var p struct{ Text string }
			if json.Unmarshal(events[i].Payload, &p) == nil {
				return p.Text
			}
		}
	}
	return ""
}

// Original context is supplied only by plan execution. Direct chat must not
// inherit the brief of an old (possibly completed) plan from the journal.
func runBrief(original, instruction string) string {
	original, instruction = strings.TrimSpace(original), strings.TrimSpace(instruction)
	if original == "" {
		return instruction
	}
	if instruction == "" || instruction == original {
		return original
	}
	return original + "\nCurrent instruction: " + instruction
}

func (l *Loop) prepareRunRegistry(ctx context.Context, sid, originalBrief string) (*tools.Registry, []string, error) {
	h := handleOf(ctx)
	if h != nil && h.registry != nil {
		return h.registry, h.skillNotes, nil
	}
	reg := l.registryFor(sid)
	if reg == nil {
		return nil, nil, fmt.Errorf("session workspace has no valid tool registry")
	}
	if l.rfx != nil {
		var errs []error
		reg, errs = reg.WithReflexes(l.rfx.List())
		if len(errs) > 0 {
			for _, registrationErr := range errs {
				if h != nil {
					if _, err := h.writer.Append(episodic.Note, map[string]string{"kind": "rfx_rejected", "text": registrationErr.Error()}); err != nil {
						return nil, nil, fmt.Errorf("record RFX rejection: %w", err)
					}
				}
			}
			// WithReflexes can return a partially composed registry. Never
			// publish or dispatch it after an enabled reflex was rejected.
			return nil, nil, fmt.Errorf("RFX registration: %v", errs)
		}
	}
	instruction := ""
	if h != nil {
		instruction = h.brief
	}
	brief := runBrief(originalBrief, instruction)
	var notes []string
	if l.skills != nil {
		for _, sk := range l.skills.Match(brief) {
			notes = append(notes, "## Loaded skill: "+sk.Name+"\n"+sk.CappedBody())
			reg = reg.WithSkills(sk.Tools)
			if h != nil {
				if _, err := h.writer.Append(episodic.Note, map[string]string{"kind": "skill_loaded", "text": "skill loaded: " + sk.Name}); err != nil {
					return nil, nil, fmt.Errorf("record skill registration: %w", err)
				}
			}
		}
	}
	if h != nil {
		// One prepared capability bundle for the entire owner, including
		// nested chat-to-plan handoff. Changes apply to the next run.
		h.registry, h.skillNotes = reg, notes
	}
	return reg, notes, nil
}

// Both chat and step loops dispatch through this phase-enforcing primitive.
// A call excluded from the model's capability set is rejected even if emitted.
func (l *Loop) executeCall(ctx context.Context, wr *episodic.Writer, reg *tools.Registry, specs []llm.ToolSpec, mode string, tc llm.ToolCall) (string, error, string) {
	if h := handleOf(ctx); h != nil {
		if err := h.boundary(ctx); err != nil {
			return "", err, ""
		}
	}
	args := json.RawMessage(tc.Function.Arguments)
	payload := map[string]any{"id": tc.ID, "name": tc.Function.Name, "raw_args": tc.Function.Arguments}
	if json.Valid(args) {
		payload["args"] = args
	}
	if _, err := wr.Append(episodic.ToolCall, payload); err != nil {
		return "", err, ""
	}
	var out string
	var callErr error
	if h := handleOf(ctx); h != nil {
		if err := h.boundary(ctx); err != nil {
			return "", err, ""
		}
		if err := h.publish("running", "tool_call", tc.Function.Name, ""); err != nil {
			return "", err, ""
		}
	}
	allowed := false
	for _, sp := range specs {
		if sp.Function.Name == tc.Function.Name {
			allowed = true
			break
		}
	}
	var object map[string]json.RawMessage
	switch {
	case !allowed:
		callErr = fmt.Errorf("tool %q was not offered for this phase", tc.Function.Name)
	case !json.Valid(args):
		callErr = fmt.Errorf("malformed tool call arguments: %s", malformedHint(tc.Function.Arguments))
	case json.Unmarshal(args, &object) != nil || object == nil:
		callErr = fmt.Errorf("tool call arguments must be a JSON object")
	default:
		out, callErr = reg.ExecuteMode(ctx, tc.Function.Name, args, mode)
	}
	if callErr != nil {
		out = strings.TrimSpace(out + "\n" + callErr.Error())
	}
	ev, err := wr.Append(episodic.ToolResult, map[string]any{"id": tc.ID, "name": tc.Function.Name, "ok": callErr == nil, "output": out})
	if err != nil {
		return out, err, ""
	}
	return out, callErr, ev.ID
}

type verifyRunner struct {
	l   *Loop
	ctx context.Context
	wr  *episodic.Writer
	reg *tools.Registry
}

func (v verifyRunner) ExecuteMode(ctx context.Context, name string, args json.RawMessage, mode string) (string, error) {
	h := handleOf(ctx)
	id := "verify"
	if h != nil {
		id = fmt.Sprintf("%s-verify-%d", h.state.ID, h.verifySeq.Add(1))
	}
	out, err, _ := v.l.executeCall(ctx, v.wr, v.reg, v.reg.Specs(mode), mode, llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: string(args)}})
	return out, err
}
func (l *Loop) verifyStep(ctx context.Context, wr *episodic.Writer, sid string, idx int, v *plan.Verify) Verdict {
	h := handleOf(ctx)
	if err := h.publish("running", "verifying", "", fmt.Sprintf("checking step %d", idx+1)); err != nil {
		return Verdict{Evidence: err.Error()}
	}
	wr.Append(episodic.Note, map[string]any{"kind": "verify_started", "index": idx, "verify": v})
	verdict := RunVerify(ctx, verifyRunner{l, ctx, wr, h.registry}, h.state.Workspace, v)
	tracker := newWorkTracker(h.state.Workspace)
	fp, _ := tracker.fingerprint()
	verdict.WorkspaceVersion = fmt.Sprintf("%x", fp)
	wr.Append(episodic.Note, map[string]any{"kind": "verify_finished", "index": idx, "verdict": verdict})
	return verdict
}
