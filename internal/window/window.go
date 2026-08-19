package window

import (
	"context"
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
	// Compacted counts turns folded into a compaction marker. Surfaced to the
	// UI so a user can SEE the session lost history — silently forgetting is
	// indistinguishable from the model ignoring what it was told.
	Compacted int `json:"compacted"`
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

// usable is the point where shedding starts. 0.75 leaves a quarter of the
// window as headroom for the reply plus the gap between our token counter and
// the engine's tokenizer (~8% measured against vLLM).
//
// It was 0.6, tuned when the window was 32K. At 96K that shed context with
// 40K still unused — the packer was throwing away history the model could
// have had.
func (m *Manager) usable() int { return int(float64(m.budget-m.reserve) * 0.75) }

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
		lastCut := -1
		for i := 0; i < cut; i++ {
			if out[i].Kind == "system" {
				continue
			}
			total -= counts[i]
			out[i].Kind = "dropped"
			rep.Trimmed++
			lastCut = i
			if total <= usable {
				break
			}
		}
		// Leave a MARKER where the history was. Dropping turns silently makes
		// the model believe the session began later than it did: it re-asks
		// settled questions and re-does finished work, with no way to tell that
		// anything is missing. The marker costs a few tokens and tells it
		// exactly what happened and where the detail still lives.
		if lastCut >= 0 {
			out[lastCut].Kind = "compaction"
			out[lastCut].Msg = llm.Message{
				Role: "user",
				Content: fmt.Sprintf("[%d earlier turns were compacted out of this window to stay "+
					"under the context limit. They are NOT lost — the full episodic log is on disk, "+
					"and files you already wrote are still in the workspace. If you need something "+
					"from before this point, re-read the files rather than assuming it never "+
					"happened.]", rep.Trimmed),
			}
			total += m.counter.Count(ctx, out[lastCut].Msg.Content) + 4
			rep.Compacted = rep.Trimmed
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
