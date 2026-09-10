package window

import (
	"cerveau/internal/llm"
	"context"
	"encoding/json"
	"fmt"
	"sync"
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
	Tokens       int    `json:"tokens"`
	Budget       int    `json:"budget"`
	Zone         string `json:"zone"`
	Demoted      int    `json:"demoted"`
	Trimmed      int    `json:"trimmed"`
	Compacted    int    `json:"compacted"`
	VisionTokens int    `json:"vision_tokens,omitempty"`
}
type Manager struct {
	mu                        sync.Mutex
	budget, reserve, keepLast int
	counter                   Counter
	resumeBrief               func(int) string
	probed                    bool
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
func (m *Manager) usable() int              { return int(float64(m.budget-m.reserve) * 0.75) }
func (m *Manager) Budget() int              { m.mu.Lock(); defer m.mu.Unlock(); return m.budget }
func (m *Manager) Sync(ctx context.Context) { m.syncBudget(ctx) }
func (m *Manager) syncBudget(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.probed {
		return
	}
	p, ok := m.counter.(ContextProber)
	if !ok {
		m.probed = true
		return
	}
	if n := p.MaxContext(ctx); n > 0 {
		m.budget = n
		m.probed = true
	}
}
func (m *Manager) SetResumeBrief(f func(int) string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resumeBrief = f
}
func (m *Manager) Build(ctx context.Context, items []Item) ([]llm.Message, Report) {
	m.mu.Lock()
	brief := m.resumeBrief
	m.mu.Unlock()
	return m.BuildWithBrief(ctx, items, brief)
}
func (m *Manager) BuildWithBrief(ctx context.Context, items []Item, brief func(int) string) ([]llm.Message, Report) {
	m.syncBudget(ctx)
	m.mu.Lock()
	budget, reserve, keep, counter := m.budget, m.reserve, m.keepLast, m.counter
	m.mu.Unlock()
	rep := Report{Budget: budget, Zone: ZoneGreen}
	usable := int(float64(budget-reserve) * .75)
	out := RepairToolGroups(items)
	count := func(msg llm.Message) int {
		n := counter.Count(ctx, msg.Content) + 4 + len(msg.Images)*llm.VisionTokensPerImage
		for _, tc := range msg.ToolCalls {
			n += counter.Count(ctx, tc.Function.Arguments) + counter.Count(ctx, tc.Function.Name)
		}
		return n
	}
	total := func() int {
		n := 0
		for _, it := range out {
			if it.Kind != "dropped" {
				n += count(it.Msg)
			}
		}
		return n
	}
	if total() > usable {
		rep.Zone = ZoneYellow
		for i := range out {
			if total() <= usable {
				break
			}
			if out[i].Kind == "tool" && out[i].EvtID != "" {
				replacement := pointerText(out[i].EvtID)
				if counter.Count(ctx, replacement) < counter.Count(ctx, out[i].Msg.Content) {
					out[i].Msg.Content = replacement
					rep.Demoted++
				}
			}
			if out[i].Kind == "assistant" && len(out[i].Msg.ToolCalls) > 0 {
				calls := append([]llm.ToolCall(nil), out[i].Msg.ToolCalls...)
				changed := false
				for j := range calls {
					if counter.Count(ctx, calls[j].Function.Arguments) > 200 {
						calls[j].Function.Arguments = `{"_elided":"historical arguments in episodic log"}`
						changed = true
					}
				}
				if changed {
					out[i].Msg.ToolCalls = calls
					rep.Demoted++
				}
			}
		}
	}
	if total() > usable {
		rep.Zone = ZoneRed
		lastCut := -1
		// Remove entire assistant/tool-result groups, never an individual half.
		for i := 0; i < len(out)-keep; i++ {
			if out[i].Kind == "system" || out[i].Kind == "pinned" || out[i].Kind == "dropped" {
				continue
			}
			end := i + 1
			if len(out[i].Msg.ToolCalls) > 0 {
				for end < len(out) && out[end].Msg.Role == "tool" {
					end++
				}
			}
			for j := i; j < end; j++ {
				out[j].Kind = "dropped"
				rep.Trimmed++
				lastCut = j
			}
			if total() <= usable {
				break
			}
			i = end - 1
		}
		if lastCut >= 0 {
			body := ""
			if brief != nil {
				body = brief(rep.Trimmed)
			}
			if body == "" {
				body = fmt.Sprintf("[%d earlier messages compacted. Full observations remain in the episodic log. Re-read current files when needed; do not replay side effects blindly.]", rep.Trimmed)
			}
			out[lastCut] = Item{Msg: llm.Message{Role: "user", Content: body}, Kind: "compaction"}
			rep.Compacted = rep.Trimmed
		}
	}
	msgs := []llm.Message{}
	for _, it := range out {
		if it.Kind != "dropped" {
			msgs = append(msgs, it.Msg)
			rep.VisionTokens += len(it.Msg.Images) * llm.VisionTokensPerImage
		}
	}
	rep.Tokens = total()
	return msgs, rep
}

// RepairToolGroups makes interruption-safe replay. Missing outcomes are explicit,
// never silently invented successes; orphaned old results are not sent to the model.
func RepairToolGroups(items []Item) []Item {
	out := []Item{}
	for i := 0; i < len(items); i++ {
		it := items[i]
		if it.Msg.Role == "tool" && it.Msg.ToolCallID != "" {
			continue
		}
		if len(it.Msg.ToolCalls) == 0 {
			out = append(out, it)
			continue
		}
		it.Msg.ToolCalls = append([]llm.ToolCall(nil), it.Msg.ToolCalls...)
		for k := range it.Msg.ToolCalls {
			if !json.Valid([]byte(it.Msg.ToolCalls[k].Function.Arguments)) {
				it.Msg.ToolCalls[k].Function.Arguments = "{}"
			}
		}
		out = append(out, it)
		results := map[string]Item{}
		j := i + 1
		for j < len(items) && items[j].Msg.Role == "tool" {
			results[items[j].Msg.ToolCallID] = items[j]
			j++
		}
		for _, tc := range it.Msg.ToolCalls {
			if r, ok := results[tc.ID]; ok {
				out = append(out, r)
			} else {
				out = append(out, Item{Kind: "tool", Msg: llm.Message{Role: "tool", ToolCallID: tc.ID, Content: "Outcome not recorded: this call was interrupted or not executed. Inspect current state before repeating any side effect."}})
			}
		}
		i = j - 1
	}
	return out
}

// Admit checks the text/schema envelope plus an explicit conservative vision
// reservation, after compaction. Base64 is not language-tokenizer input.
// Approximate counts retain another 10% headroom.
func (m *Manager) Admit(ctx context.Context, msgs []llm.Message, specs []llm.ToolSpec, reserve int) error {
	textMessages := append([]llm.Message(nil), msgs...)
	visionTokens := 0
	for i, msg := range msgs {
		if _, err := llm.ValidateImagesContext(ctx, msg.Images); err != nil {
			return err
		}
		visionTokens += len(msg.Images) * llm.VisionTokensPerImage
		textMessages[i].Images = nil
	}
	raw, err := json.Marshal(struct {
		Messages []llm.Message  `json:"messages"`
		Tools    []llm.ToolSpec `json:"tools"`
	}{textMessages, specs})
	if err != nil {
		return err
	}
	n := m.counter.Count(ctx, string(raw)) + visionTokens
	if n+n/10+reserve > m.Budget() {
		return fmt.Errorf("context blocked: request estimate %d + reply reserve %d exceeds safe capacity %d; shorten context or reduce effort", n, reserve, m.Budget())
	}
	return nil
}
func pointerText(id string) string {
	return fmt.Sprintf("[raw tool output demoted — full content at %s in the episodic log; re-run the tool if needed]", id)
}
