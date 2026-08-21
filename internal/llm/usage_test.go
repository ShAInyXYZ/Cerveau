package llm

import (
	"encoding/json"
	"testing"
)

// Usage tracked prompt and completion only, so a cached prompt looked exactly
// as expensive as a fresh one. With prefix caching on that is the difference
// between "this turn cost 30k tokens" and "this turn cost 2k and reused 28k" —
// the same number describing two very different things.
func TestUsageCapturesCachedTokens(t *testing.T) {
	// vLLM and llama.cpp both report the OpenAI shape
	raw := `{"prompt_tokens":31402,"completion_tokens":812,
	         "prompt_tokens_details":{"cached_tokens":28160}}`
	var u Usage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 31402 || u.CompletionTokens != 812 {
		t.Fatalf("base fields wrong: %+v", u)
	}
	if u.CachedTokens != 28160 {
		t.Errorf("CachedTokens = %d, want 28160 — cache reads are invisible", u.CachedTokens)
	}
}

// A response with no cache detail must not invent one.
func TestUsageWithoutCacheDetail(t *testing.T) {
	var u Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":100,"completion_tokens":20}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.CachedTokens != 0 {
		t.Errorf("CachedTokens = %d, want 0", u.CachedTokens)
	}
}

// The number that actually matters: what was NOT served from cache.
func TestFreshPromptTokens(t *testing.T) {
	u := Usage{PromptTokens: 31402, CachedTokens: 28160}
	if got := u.FreshPromptTokens(); got != 3242 {
		t.Errorf("FreshPromptTokens() = %d, want 3242", got)
	}
	// a report claiming more cached than prompt is malformed; clamp rather
	// than return a negative that would corrupt every total downstream
	odd := Usage{PromptTokens: 10, CachedTokens: 99}
	if got := odd.FreshPromptTokens(); got != 0 {
		t.Errorf("malformed usage gave %d, want 0", got)
	}
}

func TestCacheHitRate(t *testing.T) {
	u := Usage{PromptTokens: 1000, CachedTokens: 900}
	if got := u.CacheHitRate(); got < 0.89 || got > 0.91 {
		t.Errorf("CacheHitRate() = %v, want ~0.9", got)
	}
	// no prompt tokens is not a 100% hit rate, it is no data
	if got := (Usage{}).CacheHitRate(); got != 0 {
		t.Errorf("empty usage gave %v, want 0", got)
	}
}
