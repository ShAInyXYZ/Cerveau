package window

import (
	"context"
	"strings"
	"testing"

	"cerveau/internal/llm"
)

type fixedCounter struct{ perChar int }

func (c fixedCounter) Count(ctx context.Context, text string) int {
	return len(text) / (c.perChar + 1)
}

func item(kind, evt, content string) Item {
	return Item{Msg: llm.Message{Role: kind, Content: content}, EvtID: evt, Kind: kind}
}

func TestGreenKeepsEverything(t *testing.T) {
	m := NewManager(100000, 1000, fixedCounter{})
	items := []Item{
		item("system", "", "sys"),
		item("user", "evt_000001", "hello"),
		item("tool", "evt_000002", strings.Repeat("x", 500)),
	}
	msgs, rep := m.Build(context.Background(), items)
	if rep.Zone != ZoneGreen || rep.Demoted != 0 || rep.Trimmed != 0 {
		t.Fatalf("rep = %+v", rep)
	}
	if len(msgs) != 3 {
		t.Fatalf("msgs = %d", len(msgs))
	}
}

func TestYellowDemotesToolRawsOldestFirst(t *testing.T) {
	m := NewManager(1000, 100, fixedCounter{})
	big := strings.Repeat("x", 400)
	items := []Item{
		item("system", "", "sys"),
		item("tool", "evt_000001", big),
		item("tool", "evt_000002", big),
		item("user", "evt_000003", "question"),
	}
	msgs, rep := m.Build(context.Background(), items)
	if rep.Zone == ZoneGreen {
		t.Fatalf("expected pressure, rep = %+v", rep)
	}
	if rep.Demoted == 0 {
		t.Fatal("expected demotions")
	}
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "[raw tool output demoted") && !strings.Contains(msg.Content, "evt_000001") {
			t.Fatalf("pointer missing evt id: %q", msg.Content)
		}
	}
}

func TestRedTrimsToTail(t *testing.T) {
	m := NewManager(300, 50, fixedCounter{})
	big := strings.Repeat("x", 300)
	items := []Item{item("system", "", "sys")}
	for i := 0; i < 20; i++ {
		items = append(items, item("user", "evt", big))
	}
	msgs, rep := m.Build(context.Background(), items)
	if rep.Zone != ZoneRed {
		t.Fatalf("zone = %s, want red", rep.Zone)
	}
	if rep.Trimmed == 0 {
		t.Fatal("expected trims")
	}
	sys := 0
	for _, msg := range msgs {
		if msg.Role == "system" {
			sys++
		}
	}
	if sys != 1 {
		t.Fatal("system message must survive the red zone")
	}
	if len(msgs) > m.keepLast+1 {
		t.Fatalf("tail not enforced: %d msgs", len(msgs))
	}
}

func TestPointerTextReferencesEvent(t *testing.T) {
	p := pointerText("evt_000042")
	if !strings.Contains(p, "evt_000042") {
		t.Fatalf("pointer = %q", p)
	}
}

// An assistant message's TOOL CALL ARGUMENTS count toward the window. They
// are often far larger than its text content (whole-file writes), and
// ignoring them let the manager believe a 33k request fit in a 32k budget.
func TestToolCallArgsAreCounted(t *testing.T) {
	big := strings.Repeat("x", 40000) // ~10k tokens of arguments
	items := []Item{
		{Msg: llm.Message{Role: "system", Content: "sys"}, Kind: "system"},
		{Msg: llm.Message{
			Role:    "assistant",
			Content: "", // no text at all — only the call
			ToolCalls: []llm.ToolCall{{
				ID:       "c1",
				Function: llm.FunctionCall{Name: "write", Arguments: big},
			}},
		}, Kind: "assistant", EvtID: "evt_1"},
	}
	m := NewManager(4000, 500, CounterFunc(func(_ context.Context, s string) int { return len(s) / 4 }))
	msgs, rep := m.Build(context.Background(), items)
	// Counted => over budget => the oversized arguments get elided.
	if rep.Demoted == 0 {
		t.Fatal("oversized tool call arguments were neither counted nor demoted")
	}
	for _, msg := range msgs {
		for _, tc := range msg.ToolCalls {
			if len(tc.Function.Arguments) > 500 {
				t.Fatalf("arguments still %d chars in the sent window", len(tc.Function.Arguments))
			}
		}
	}
}
