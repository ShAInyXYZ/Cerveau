package loop

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cerveau/internal/episodic"
)

func TestCompletedDecodeBeforeControlStillConsumesEffort(t *testing.T) {
	for _, path := range []string{"turn", "step"} {
		t.Run(path, func(t *testing.T) {
			var owner atomic.Pointer[Loop]
			l, _, calls := setup(t, func(call int) map[string]any {
				// Model completion races with a real control flag after decode.
				// Set the flag without cancelling transport, preserving usage.
				if call <= 2 {
					owner.Load().runs.get("s1").steered.Store(true)
				}
				generated := maxTurnTokens * (maxTokenExtensions + 1) / 2
				return map[string]any{
					"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "discarded reply"}, "finish_reason": "stop"}},
					"usage":   map[string]any{"completion_tokens": generated, "completion_tokens_details": map[string]int{"reasoning_tokens": generated}},
				}
			})
			owner.Store(l)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if path == "turn" {
				result, err := l.Run(ctx, "s1", "continue investigating", "discussion")
				if err != nil || result.StopReason != StopTokens || !strings.Contains(result.Reply, "including reasoning") {
					t.Fatalf("controlled decode escaped turn guard: %+v err=%v", result, err)
				}
			} else {
				ws := t.TempDir()
				l.SetWorkspaceFunc(func(string) string { return ws })
				ctx, h, finish, err := l.beginRun(ctx, "s1", "autopilot", "continue investigating")
				if err != nil {
					t.Fatal(err)
				}
				defer finish(nil, nil)
				h.registry = l.registry()
				p := &Plan{Title: "bounded effort", Steps: []PlanStep{{Title: "investigate"}}}
				if _, err := h.writer.Append(episodic.Plan, p); err != nil {
					t.Fatal(err)
				}
				_, err = l.runStep(ctx, h.writer, "s1", "Investigate.", ModeByName("autopilot"), p, 0, nil, StepPrompt{Step: p.Steps[0]})
				if err == nil || !strings.Contains(err.Error(), "including reasoning") {
					t.Fatalf("controlled decode escaped step guard: %v", err)
				}
			}
			if *calls != 2 {
				t.Fatalf("calls=%d; cumulative effort must stop before a third decode", *calls)
			}
		})
	}
}

func TestTurnAndPlanningEmptyTruncationShareOneRetry(t *testing.T) {
	for _, mode := range []string{"discussion", "autopilot"} {
		for _, finishes := range [][2]string{{"length", "length"}, {"length", "stop"}, {"stop", "length"}} {
			t.Run(fmt.Sprintf("%s/%s-%s", mode, finishes[0], finishes[1]), func(t *testing.T) {
				empty := func(finish string) map[string]any {
					return map[string]any{"finish_reason": finish, "message": map[string]any{"role": "assistant", "content": ""}}
				}
				m := newScriptedModel(empty(finishes[0]), empty(finishes[1]), textReply("unexpected redundant third decode"))
				defer m.srv.Close()
				l, _ := gateFixture(t, m)
				l.SetThinking("always", "xhigh")
				result, err := l.Run(context.Background(), "s1", "Build a skylight fixture", mode)
				wantStop := StopLLMError
				if mode == "autopilot" {
					wantStop = "planning_blocked"
				}
				if err != nil || result.StopReason != wantStop || len(m.bodies) != 2 {
					t.Fatalf("stacked empty retries: result=%+v err=%v calls=%d", result, err, len(m.bodies))
				}
				if finishes[0] == "length" && !strings.Contains(m.bodies[1], `"enable_thinking":false`) {
					t.Fatal("truncation retry retained expensive thinking")
				}
				if finishes[1] == "length" && !strings.Contains(result.Reply, "output limit") {
					t.Fatalf("lost output-limit stop reason: %+v", result)
				}
			})
		}
	}
}

func TestTurnAndPlanningDoNotRetryLengthEmptyWhenThinkingAlreadyOff(t *testing.T) {
	for _, mode := range []string{"discussion", "autopilot"} {
		t.Run(mode, func(t *testing.T) {
			m := newScriptedModel(emptyLengthReply(), textReply("unexpected redundant retry"))
			defer m.srv.Close()
			l, _ := gateFixture(t, m)
			l.SetThinking("off", "low")
			result, err := l.Run(context.Background(), "s1", "Build a skylight fixture", mode)
			if err != nil || len(m.bodies) != 1 || !strings.Contains(result.Reply, "output limit") {
				t.Fatalf("retried unchanged output capacity: result=%+v err=%v calls=%d", result, err, len(m.bodies))
			}
		})
	}
}

func TestPlainEmptyTurnStillGetsOneUsefulRetry(t *testing.T) {
	m := newScriptedModel(textReply(""), textReply("answer recovered"))
	defer m.srv.Close()
	l, _ := gateFixture(t, m)
	result, err := l.Run(context.Background(), "s1", "Explain skylight", "discussion")
	if err != nil || result.Reply != "answer recovered" || len(m.bodies) != 2 {
		t.Fatalf("ordinary empty reply lost recovery: result=%+v err=%v calls=%d", result, err, len(m.bodies))
	}
}
