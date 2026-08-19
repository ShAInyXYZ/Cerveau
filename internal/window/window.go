package window

import (
	"context"
	"encoding/json"
	"fmt"

	"cerveau/internal/llm"
)

const (
	ZoneGreen  = "green"
	ZoneYellow = "yellow"
	ZoneRed    = "red"
)

type Item struct {
	Msg   llm.Message
	EvtID string
	Kind  string
}

type Report struct {
	Tokens  int    `json:"tokens"`
	Budget  int    `json:"budget"`
	Zone    string `json:"zone"`
	Demoted int    `json:"demoted"`
	Trimmed int    `json:"trimmed"`
}

type Manager struct {
	budget   int
	reserve  int
	keepLast int
	counter  Counter
}

func NewManager(budget, reserve int, counter Counter) *Manager {
	if budget <= 0 {
		budget = 32768
	}
	if reserve <= 0 {
		reserve = 2048
	}
	return &Manager{budget: budget, reserve: reserve, keepLast: 6, counter: counter}
}

// Budget is the full context window the model was configured with. Callers
// need it to size an output cap that fits alongside the prompt.
func (m *Manager) Budget() int { return m.budget }

// CountMessages estimates the token cost of an outgoing message list, using
// the same counter the packer uses. Callers cap max_tokens against it: vLLM
// rejects a request whose prompt + max_tokens exceeds the window, where
// llama.cpp silently truncates.
// CountRequest counts everything that goes into a completion request:
// the messages AND the tool specs.
//
// The specs are the part that used to be missed. They are resent verbatim on
// every request and each one carries a full JSON schema, so on a rich tool
// registry they are worth thousands of tokens. Counting only messages made the
// output clamp believe there was ~10k more room than there was, and vLLM —
// which rejects prompt+max_tokens > window instead of truncating — 502'd on a
// total of exactly 32769.
func (m *Manager) CountRequest(msgs []llm.Message, specs []llm.ToolSpec) int {
	n := m.CountMessages(msgs)
	if m.counter == nil {
		return n
	}
	ctx := context.Background()
	for _, s := range specs {
		n += m.counter.Count(ctx, s.Function.Name)
		n += m.counter.Count(ctx, s.Function.Description)
		if b, err := json.Marshal(s.Function.Parameters); err == nil {
			n += m.counter.Count(ctx, string(b))
		}
		n += 4 // per-tool framing
	}
	return n
}

func (m *Manager) CountMessages(msgs []llm.Message) int {
	if m.counter == nil {
		return 0
	}
	ctx := context.Background()
	n := 0
	for _, msg := range msgs {
		n += m.counter.Count(ctx, msg.Content) + 4
		for _, tc := range msg.ToolCalls {
			n += m.counter.Count(ctx, tc.Function.Arguments) + m.counter.Count(ctx, tc.Function.Name)
		}
	}
	return n
}

func (m *Manager) usable() int { return int(float64(m.budget-m.reserve) * 0.6) }

func (m *Manager) Build(ctx context.Context, items []Item) ([]llm.Message, Report) {
	rep := Report{Budget: m.budget, Zone: ZoneGreen}
	counts := make([]int, len(items))
	total := 0
	for i, it := range items {
		// Tool-call ARGUMENTS are part of the request too, and for a
		// whole-file write they dwarf the assistant's text (which is often
		// empty). Counting only Content let a 33k request look like 1k and
		// sail past the budget into "exceeds the available context size".
		n := m.counter.Count(ctx, it.Msg.Content) + 4
		for _, tc := range it.Msg.ToolCalls {
			n += m.counter.Count(ctx, tc.Function.Arguments) + m.counter.Count(ctx, tc.Function.Name)
		}
		counts[i] = n
		total += n
	}
	rep.Tokens = total
	usable := m.usable()
	out := make([]Item, len(items))
	copy(out, items)

	if total > usable {
		rep.Zone = ZoneYellow
		for i := range out {
			if total <= usable {
				break
			}
			if out[i].Kind == "tool" && out[i].EvtID != "" {
				out[i].Msg.Content = pointerText(out[i].EvtID)
				total -= counts[i] - m.counter.Count(ctx, out[i].Msg.Content)
				rep.Demoted++
				continue
			}
			// A replayed assistant turn whose tool call carried a huge payload
			// (a whole-file write) is history: the model does not need the
			// arguments again, only that the call happened. Stub them.
			if out[i].Kind == "assistant" && len(out[i].Msg.ToolCalls) > 0 {
				before := counts[i]
				calls := make([]llm.ToolCall, len(out[i].Msg.ToolCalls))
				copy(calls, out[i].Msg.ToolCalls)
				shrunk := false
				for j := range calls {
					if m.counter.Count(ctx, calls[j].Function.Arguments) > 200 {
						calls[j].Function.Arguments = `{"_elided":"arguments dropped from the window — see the episodic log"}`
						shrunk = true
					}
				}
				if shrunk {
					out[i].Msg.ToolCalls = calls
					after := m.counter.Count(ctx, out[i].Msg.Content) + 4
					for _, tc := range calls {
						after += m.counter.Count(ctx, tc.Function.Arguments) + m.counter.Count(ctx, tc.Function.Name)
					}
					total -= before - after
					rep.Demoted++
				}
			}
		}
	}
	if total > usable {
		rep.Zone = ZoneRed
		cut := len(out) - m.keepLast
		for i := 0; i < cut; i++ {
			if out[i].Kind == "system" {
				continue
			}
			total -= counts[i]
			out[i].Kind = "dropped"
			rep.Trimmed++
			if total <= usable {
				break
			}
		}
	}
	msgs := []llm.Message{}
	for _, it := range out {
		if it.Kind == "dropped" {
			continue
		}
		msgs = append(msgs, it.Msg)
	}
	rep.Tokens = total
	return msgs, rep
}

func pointerText(evtID string) string {
	return fmt.Sprintf("[raw tool output demoted — full content at %s in the episodic log; re-run the tool if needed]", evtID)
}
