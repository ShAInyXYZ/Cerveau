package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The request carries the level the turn asked for: off means the template
// flag is false; a level means enable_thinking plus reasoning_effort, and a
// bigger per-call cap so the answer fits after the reasoning.
func TestThinkingShapesTheRequest(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		// vLLM 0.27 shape: `reasoning`, no completion_tokens_details
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok","reasoning":"` + strings.Repeat("think ", 40) + `"}}],
		  "usage":{"prompt_tokens":10,"completion_tokens":70}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	msg, u, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	kw := got["chat_template_kwargs"].(map[string]any)
	if kw["enable_thinking"] != false || got["max_tokens"].(float64) != 100 {
		t.Fatalf("default must be thinking off with the plain cap, got %v %v", kw, got["max_tokens"])
	}
	// 240 chars of reasoning ≈ 60 tokens estimated; 70 - 60 = 10 answer tokens
	if !strings.HasPrefix(msg.Reasoning, "think ") || u.ReasoningTokens != 60 || u.AnswerTokens() != 10 {
		t.Fatalf("reasoning not parsed/estimated: %q %d %d", msg.Reasoning[:12], u.ReasoningTokens, u.AnswerTokens())
	}

	_, _, err = c.Complete(WithThinking(context.Background(), ThinkingMedium), []Message{{Role: "user", Content: "hi"}}, nil, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	kw = got["chat_template_kwargs"].(map[string]any)
	if kw["enable_thinking"] != true || kw["reasoning_effort"] != "medium" || got["max_tokens"].(float64) != float64(100+BudgetFor(ThinkingMedium)) {
		t.Fatalf("medium thinking should enable, set effort and grow the cap, got %v %v", kw, got["max_tokens"])
	}

	c.SetThinking("xhigh")
	_, _, _ = c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "", 100)
	if got["chat_template_kwargs"].(map[string]any)["reasoning_effort"] != "xhigh" {
		t.Fatal("session default should apply when the context sets nothing")
	}
	_, _, _ = c.Complete(WithThinking(context.Background(), ThinkingOff), []Message{{Role: "user", Content: "hi"}}, nil, "", 100)
	if got["chat_template_kwargs"].(map[string]any)["enable_thinking"] != false {
		t.Fatal("an explicit off on the context must beat the session default")
	}
}

// A reply cut off at max_tokens with nothing said is not an answer.
func TestTruncatedReplyIsNotAnAnswer(t *testing.T) {
	if !(Message{FinishReason: "length"}).Truncated() {
		t.Fatal("length + empty = truncated")
	}
	if (Message{FinishReason: "length", Content: "done"}).Truncated() {
		t.Fatal("a cut-off reply that still said something is an answer to judge, not a blank")
	}
	if (Message{FinishReason: "stop"}).Truncated() {
		t.Fatal("stop with empty content is empty, not truncated")
	}
}

func TestThinkingBudgetsAndStepDown(t *testing.T) {
	if BudgetFor(ThinkingLow) >= BudgetFor(ThinkingMedium) || BudgetFor(ThinkingMedium) >= BudgetFor(ThinkingXHigh) {
		t.Fatal("budgets must grow with the level")
	}
	if StepDown(ThinkingXHigh) != ThinkingMedium || StepDown(ThinkingMedium) != ThinkingLow || StepDown(ThinkingLow) != ThinkingOff || StepDown(ThinkingOff) != ThinkingOff {
		t.Fatal("step-down order is xhigh → medium → low → off")
	}
	if ThinkingBudgetOf(WithThinkingBudget(context.Background(), 20000)) != 20000 {
		t.Fatal("budget override not carried on the context")
	}
}

// "default" must send no sampling fields at all, so the Core applies the
// model's generation_config; the tuned presets must still send theirs.
func TestDefaultSamplingSendsNothing(t *testing.T) {
	t.Setenv("CRV_TEMP", "")
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	if c.SamplingName() != "default" {
		t.Fatalf("new client sampling = %q, want default", c.SamplingName())
	}
	c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "", 100)
	if _, ok := got["temperature"]; ok {
		t.Fatalf("default must not send temperature: %v", got["temperature"])
	}
	if _, ok := got["top_p"]; ok {
		t.Fatalf("default must not send top_p: %v", got["top_p"])
	}

	c.SetSampling("strict")
	c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "", 100)
	if got["temperature"] != 0.2 {
		t.Fatalf("strict must still send 0.2, got %v", got["temperature"])
	}
}
