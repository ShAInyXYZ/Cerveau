package window

import (
	"context"
	"strings"
	"testing"

	"cerveau/internal/llm"
)

// Stage 3 today is a silent delete: over budget after demoting, Build marks the
// oldest items "dropped" and they vanish. The model loses what it decided and
// why, with nothing left in its place — the turn simply forgets.
//
// A compaction must leave a MARKER behind, so the model knows history was
// removed rather than believing the session started later than it did.
func TestDroppedTurnsLeaveACompactionMarker(t *testing.T) {
	m := NewManager(2000, 100, fixedCounter{perChar: 1})
	items := []Item{{Kind: "system", Msg: llm.Message{Role: "system", Content: "sys"}}}
	for i := 0; i < 40; i++ {
		items = append(items, Item{Kind: "user", Msg: llm.Message{
			Role: "user", Content: strings.Repeat("decision about the schema ", 40),
		}})
	}

	msgs, rep := m.Build(context.Background(), items)

	if rep.Trimmed == 0 {
		t.Fatal("nothing trimmed — the fixture is not over budget")
	}
	joined := ""
	for _, msg := range msgs {
		joined += msg.Content + "\n"
	}
	if !strings.Contains(joined, "compacted") {
		t.Errorf("history was deleted with no trace — the model cannot know it lost context.\ngot:\n%s",
			joined[:min(400, len(joined))])
	}
	if rep.Compacted == 0 {
		t.Error("Report.Compacted is 0 — the UI has nothing to display")
	}
}

// A conversation that fits must be untouched: no marker, no compaction.
func TestSmallConversationIsNotCompacted(t *testing.T) {
	m := NewManager(100000, 100, fixedCounter{perChar: 1})
	items := []Item{
		{Kind: "system", Msg: llm.Message{Role: "system", Content: "sys"}},
		{Kind: "user", Msg: llm.Message{Role: "user", Content: "hello"}},
	}
	msgs, rep := m.Build(context.Background(), items)
	if rep.Compacted != 0 || rep.Trimmed != 0 {
		t.Errorf("compacted a conversation that fits: %+v", rep)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
