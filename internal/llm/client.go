package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// Reasoning is the model's thinking, split out by the Core's reasoning
	// parser when thinking is on. Never sent back: the window is rebuilt from
	// the episodic log, and the template handles history without it.
	Reasoning string `json:"reasoning_content,omitempty"`
	// ReasoningAlt: vLLM 0.27 names the field `reasoning`; older servers and
	// llama.cpp use `reasoning_content`. Folded into Reasoning after decode.
	ReasoningAlt string `json:"reasoning,omitempty"`
	// FinishReason is why the model stopped: "stop", "tool_calls", or
	// "length" — cut off at max_tokens. Never sent back.
	FinishReason string `json:"-"`
	// Raw is the first 2 KB of the server's response, kept ONLY when the
	// reply is empty (no text, no tool call) so the log can show what the
	// tool parser swallowed. Never sent back.
	Raw string `json:"-"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolSpec struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatRequest struct {
	Model       string         `json:"model"`
	Messages    []Message      `json:"messages"`
	Tools       []ToolSpec     `json:"tools,omitempty"`
	Grammar     string         `json:"grammar,omitempty"`
	Temperature float64        `json:"temperature,omitempty"`
	TopP        float64        `json:"top_p,omitempty"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	TemplateKW  map[string]any `json:"chat_template_kwargs,omitempty"`
	ToolChoice  any            `json:"tool_choice,omitempty"`
}

// Usage is what one model call cost.
//
// CachedTokens matters as much as the totals: with prefix caching on, a prompt
// that is 90% cache read is nothing like a prompt of the same size sent fresh,
// and a single "31,402 prompt tokens" figure describes both. Claw Code's
// usage.rs tracks cache reads separately for the same reason.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`

	// CachedTokens is filled from prompt_tokens_details.cached_tokens, the
	// OpenAI shape.
	//
	// MEASURED 2026-08-21: the vLLM Core in use here returns
	// "prompt_tokens_details": null on every response, so this stays 0 even
	// when the prefix cache is plainly working. Zero therefore means "no hit
	// OR not reported" and must never be rendered as a confident 0% hit rate.
	// The field is parsed anyway because it costs nothing and a newer vLLM,
	// llama.cpp, or a hosted endpoint will fill it.
	CachedTokens int `json:"-"`
	// ReasoningTokens is the thinking part of the completion, from
	// completion_tokens_details.reasoning_tokens. They are generated but
	// never re-sent, so the turn's window budget must not count them.
	ReasoningTokens int `json:"-"`

	Details *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
	CompletionDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`
}

// UnmarshalJSON lifts the nested cache count into a flat field, so every
// consumer reads one struct rather than nil-checking a pointer.
func (u *Usage) UnmarshalJSON(b []byte) error {
	type raw Usage // alias sheds the custom method, avoiding recursion
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*u = Usage(r)
	if u.Details != nil {
		u.CachedTokens = u.Details.CachedTokens
	}
	if u.CompletionDetails != nil {
		u.ReasoningTokens = u.CompletionDetails.ReasoningTokens
	}
	return nil
}

// AnswerTokens is what the completion cost the WINDOW: reasoning is dropped
// after the call, the answer and tool calls come back on every later turn.
func (u Usage) AnswerTokens() int {
	if u.ReasoningTokens > u.CompletionTokens {
		return 0
	}
	return u.CompletionTokens - u.ReasoningTokens
}

// FreshPromptTokens is the prompt work actually done — total minus what was
// served from cache. This is the number that reflects effort.
func (u Usage) FreshPromptTokens() int {
	n := u.PromptTokens - u.CachedTokens
	if n < 0 {
		return 0 // malformed report; a negative would corrupt every total
	}
	return n
}

// CacheHitRate is the share of the prompt served from cache, 0 when there is
// nothing to divide by. No prompt tokens is no data, not a perfect score.
func (u Usage) CacheHitRate() float64 {
	if u.PromptTokens <= 0 {
		return 0
	}
	return float64(u.CachedTokens) / float64(u.PromptTokens)
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type Client struct {
	base     string
	key      string
	model    string
	thinking string // session default thinking level; "" or ThinkingOff = off
	// session default; a request may override it (see samplingFor)
	sampling Sampling
	http     *http.Client
}

// NewClient targets an OpenAI-compatible endpoint. CRV_MODEL_KEY supplies a
// bearer token when the Core behind it requires one — llama.cpp serves
// unauthenticated, but a vLLM Core started with VLLM_API_KEY answers 401
// without it. Empty means no header, which is the llama.cpp case.
func NewClient(base string) *Client {
	// llama.cpp ignores the model name and answers to anything ("local" by
	// convention). vLLM checks it against --served-model-name and 502s with
	// "The model `local` does not exist" otherwise, so a Core can override it.
	model := strings.TrimSpace(os.Getenv("CRV_MODEL_NAME"))
	if model == "" {
		model = "local"
	}
	// CRV_TEMP seeds the SESSION DEFAULT only. It is no longer the last word:
	// the panel changes it live, and a single turn can override it, because
	// temperature is a per-request field and never needed a restart.
	return &Client{
		base:     base,
		key:      strings.TrimSpace(os.Getenv("CRV_MODEL_KEY")),
		model:    model,
		sampling: Preset(os.Getenv("CRV_TEMP")),
		http:     &http.Client{Timeout: 10 * time.Minute, Transport: CoreTransport()},
	}
}

func (c *Client) Complete(ctx context.Context, messages []Message, tools []ToolSpec, grammar string, maxTokens int) (Message, Usage, error) {
	return c.CompleteWith(ctx, messages, tools, grammar, maxTokens, "")
}

// CompleteWith is Complete plus a one-turn sampling override. Empty keeps the
// session default.
func (c *Client) CompleteWith(ctx context.Context, messages []Message, tools []ToolSpec, grammar string, maxTokens int, sampling string) (Message, Usage, error) {
	sp := c.samplingFor(sampling)
	// Thinking. Off was the only setting for a long time: on the single-card
	// Core a <think> block ate the whole per-call budget and the truncated
	// buffer parsed as a malformed tool call. It is now a level — off, low,
	// medium, xhigh — chosen per call (ThinkingOf(ctx)) or as the session
	// default. When on, the per-call cap grows by a thinking budget so the
	// answer still fits after the reasoning, and reasoning_effort bounds it.
	level := ThinkingOf(ctx)
	if level == "" {
		level = c.thinking
	}
	kw := map[string]any{"enable_thinking": false}
	if level != "" && level != ThinkingOff {
		kw = map[string]any{"enable_thinking": true, "reasoning_effort": level}
		budget := BudgetFor(level)
		if b := ThinkingBudgetOf(ctx); b > 0 {
			budget = b
		}
		maxTokens += budget
	}
	body := chatRequest{
		Model:      c.model,
		Messages:   messages,
		Tools:      tools,
		Grammar:    grammar,
		MaxTokens:  maxTokens,
		TemplateKW: kw,
	}
	if ft := ForcedToolOf(ctx); ft != "" {
		body.ToolChoice = map[string]any{"type": "function", "function": map[string]string{"name": ft}}
	}
	// Unset leaves the field out of the request entirely, so the Core falls
	// back to the model's generation_config rather than to an OpenAI default.
	if sp.Temp != Unset {
		body.Temperature = sp.Temp
	}
	if sp.TopP != Unset {
		body.TopP = sp.TopP
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Message{}, Usage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return Message{}, Usage{}, err
	}
	req.Header.Set("content-type", "application/json")
	if c.key != "" {
		req.Header.Set("authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("llm unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, Usage{}, err
	}
	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return Message{}, Usage{}, fmt.Errorf("parse llm response: %w", err)
	}
	if out.Error != nil {
		return Message{}, Usage{}, fmt.Errorf("llm error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return Message{}, Usage{}, fmt.Errorf("llm returned no choices")
	}
	msg := out.Choices[0].Message
	msg.FinishReason = out.Choices[0].FinishReason
	if len(msg.ToolCalls) == 0 && strings.TrimSpace(msg.Content) == "" {
		if len(data) > 2048 {
			msg.Raw = string(data[:2048]) + "…"
		} else {
			msg.Raw = string(data)
		}
	}
	if msg.Reasoning == "" && msg.ReasoningAlt != "" {
		msg.Reasoning = msg.ReasoningAlt
	}
	msg.ReasoningAlt = ""
	// This vLLM reports no completion_tokens_details. The reasoning is in
	// hand, so estimate its share (~4 chars per token) rather than charge
	// the whole think block to the window budget.
	if out.Usage.ReasoningTokens == 0 && msg.Reasoning != "" {
		out.Usage.ReasoningTokens = len(msg.Reasoning) / 4
		if out.Usage.ReasoningTokens > out.Usage.CompletionTokens {
			out.Usage.ReasoningTokens = out.Usage.CompletionTokens
		}
	}
	return msg, out.Usage, nil
}

// CoreTransport keeps idle connections for less time than the Core's server
// does. vLLM (uvicorn) closes an idle keep-alive after ~5 s; a turn spends far
// longer than that in tools between two model calls, so the next POST went
// out on a connection the server had already closed and came back as a bare
// EOF — "model call failed — retrying", five times in one chess build
// (2026-09-04). Go retries idempotent requests on a dead connection; a POST
// is not one. Dropping idle connections after 2 s means every model call
// after a pause opens fresh, at the cost of one local TCP handshake.
func CoreTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.IdleConnTimeout = 2 * time.Second
	return t
}

// Truncated reports a reply that ran out of tokens before saying anything:
// no tool call, no text, finish_reason length. With thinking on, that is
// the model still reasoning when the cap hit. The Crane session (2026-09-04)
// closed two turns this way — 16,384 tokens of reasoning each, empty text,
// and the panel played the done sound over nothing.
func (m Message) Truncated() bool {
	return m.FinishReason == "length" && len(m.ToolCalls) == 0 && strings.TrimSpace(m.Content) == ""
}
