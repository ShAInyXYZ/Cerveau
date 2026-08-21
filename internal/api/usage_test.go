package api

import (
	"encoding/json"
	"testing"

	"cerveau/internal/episodic"
)

// The whole point of recording usage: answering "what did this session cost"
// from the log, rather than from a benchmark run by hand.
func TestSessionUsageSumsTheLog(t *testing.T) {
	events := []episodic.Event{
		mkAssistant(t, 1000, 200, 800),
		mkAssistant(t, 2000, 300, 1900),
		{Type: episodic.MsgUser, Payload: json.RawMessage(`{"text":"hi"}`)},
	}
	got := sumUsage(events)

	if got.PromptTokens != 3000 || got.CompletionTokens != 500 {
		t.Errorf("totals wrong: %+v", got)
	}
	if got.CachedTokens != 2700 {
		t.Errorf("CachedTokens = %d, want 2700", got.CachedTokens)
	}
	// 3000 prompt, 2700 of it cached -> only 300 was fresh work
	if got.FreshPromptTokens != 300 {
		t.Errorf("FreshPromptTokens = %d, want 300", got.FreshPromptTokens)
	}
	if got.Calls != 2 {
		t.Errorf("Calls = %d, want 2", got.Calls)
	}
}

// A log written before usage was recorded must report zero, not crash.
func TestSessionUsageOnAnOldLog(t *testing.T) {
	events := []episodic.Event{
		{Type: episodic.MsgAssistant, Payload: json.RawMessage(`{"text":"no usage here"}`)},
	}
	got := sumUsage(events)
	if got.Calls != 0 || got.PromptTokens != 0 {
		t.Errorf("old log should total zero, got %+v", got)
	}
}

func mkAssistant(t *testing.T, prompt, completion, cached int) episodic.Event {
	t.Helper()
	p, _ := json.Marshal(map[string]any{
		"text": "ok",
		"usage": map[string]any{
			"prompt_tokens": prompt, "completion_tokens": completion,
			"cached_tokens": cached, "fresh_prompt_tokens": prompt - cached,
		},
	})
	return episodic.Event{Type: episodic.MsgAssistant, Payload: p}
}
