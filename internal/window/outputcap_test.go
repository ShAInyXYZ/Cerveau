package window

import (
	"context"
	"testing"

	"cerveau/internal/llm"
)

type charCounter struct{}

func (charCounter) Count(_ context.Context, s string) int { return len(s) / 4 }

// A Core must not require the harness to know which engine it is.
//
// llama.cpp treats an over-large max_tokens as advisory and truncates. vLLM
// rejects the request:
//
//	"requested 8192 output tokens and your prompt contains at least 24577
//	 input tokens, for a total of at least 32769 tokens"
//
// So the caller sizes the output cap against the live prompt. This pins the
// arithmetic that makes that possible.
func TestCountMessagesEnablesAnOutputCapThatFits(t *testing.T) {
	m := NewManager(32768, 2048, charCounter{})

	big := make([]byte, 90_000) // ~22.5k tokens at 4 chars/token
	for i := range big {
		big[i] = 'x'
	}
	msgs := []llm.Message{{Role: "user", Content: string(big)}}

	used := m.CountMessages(msgs)
	if used == 0 {
		t.Fatal("CountMessages returned 0 for a large prompt")
	}
	room := m.Budget() - used - 256
	if room <= 0 {
		t.Fatalf("no room left (budget %d, used %d) — prompt should still fit", m.Budget(), used)
	}
	if used+room+256 > m.Budget() {
		t.Errorf("prompt %d + cap %d + margin exceeds budget %d — vLLM would 400 this",
			used, room, m.Budget())
	}
}

func TestBudgetIsTheConfiguredWindow(t *testing.T) {
	if got := NewManager(32768, 2048, charCounter{}).Budget(); got != 32768 {
		t.Errorf("Budget() = %d, want 32768", got)
	}
}

// The prompt vLLM sees is messages + TOOL SPECS. CountMessages ignored the
// specs, so the clamp believed the prompt was ~10k tokens smaller than it was
// and let prompt+max_tokens overflow the window:
//
//   "requested 6960 output tokens and your prompt contains at least 25809
//    input tokens, for a total of at least 32769 tokens" (502)
//
// Every tool schema is resent on every single request, so this is not a small
// correction — it is most of the gap the 10k margin was silently absorbing.
func TestCountRequestIncludesToolSpecs(t *testing.T) {
	m := NewManager(32768, 2048, charCounter{})
	msgs := []llm.Message{{Role: "user", Content: "hello"}}
	specs := []llm.ToolSpec{
		{Type: "function", Function: llm.FunctionSpec{
			Name:        "write",
			Description: "write a file to disk",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
			},
		}},
	}

	msgOnly := m.CountMessages(msgs)
	withSpecs := m.CountRequest(msgs, specs)

	if withSpecs <= msgOnly {
		t.Fatalf("tool specs contribute nothing to the count: messages=%d withSpecs=%d", msgOnly, withSpecs)
	}
}
