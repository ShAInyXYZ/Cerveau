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

func (m *Manager) usable() int { return int(float64(m.budget-m.reserve) * 0.6) }

func (m *Manager) Build(ctx context.Context, items []Item) ([]llm.Message, Report) {
	rep := Report{Budget: m.budget, Zone: ZoneGreen}
	counts := make([]int, len(items))
	total := 0
	for i, it := range items {
		n := m.counter.Count(ctx, it.Msg.Content) + 4
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
