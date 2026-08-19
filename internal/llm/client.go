package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
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
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
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
	base  string
	key   string
	model string
	temp  float64
	topP  float64
	http  *http.Client
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
	// Sampling. CRV_TEMP names a preset or a raw value.
	//   strict 0.4 · neutral 0.55 · creative 0.7
	// Qwen's own guidance is 0.7/top_p 0.8 for instruct and 1.0/0.95 with
	// thinking, so these presets sit at or below its instruct setting —
	// deliberately tighter, because this harness writes code.
	temp, topP := 0.2, 0.0
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CRV_TEMP"))) {
	case "strict":
		// 0.2 / no top_p — MEASURED, not chosen. This was the default before
		// the slider existed, and the benchmark run that produced the best
		// output used it. A 0.4/0.8 "strict" preset lost visibly on all four
		// projects: less complete, less correct. Qwen's own guidance (0.7/0.8
		// instruct) is for chat; code generation wants the tighter setting.
		temp, topP = 0.2, 0.0
	case "neutral":
		temp, topP = 0.55, 0.85
	case "creative":
		temp, topP = 0.7, 0.9
	case "":
		// unset: keep the long-standing 0.2 default
	default:
		if v, err := strconv.ParseFloat(os.Getenv("CRV_TEMP"), 64); err == nil && v >= 0 && v <= 2 {
			temp = v
		}
	}
	return &Client{
		base:  base,
		key:   strings.TrimSpace(os.Getenv("CRV_MODEL_KEY")),
		model: model,
		temp:  temp,
		topP:  topP,
		http:  &http.Client{Timeout: 10 * time.Minute},
	}
}

func (c *Client) Complete(ctx context.Context, messages []Message, tools []ToolSpec, grammar string, maxTokens int) (Message, Usage, error) {
	body := chatRequest{
		Model:       c.model,
		Messages:    messages,
		Tools:       tools,
		Grammar:     grammar,
		Temperature: c.temp,
		TopP:        c.topP,
		MaxTokens:   maxTokens,
		// Qwen3 is a reasoning model: left on, it burns the entire token budget
		// inside a <think> block and never emits an answer (finish_reason=length,
		// empty content) — and --jinja then mis-parses the truncated buffer as a
		// malformed tool call. This harness wants straight-to-the-point output,
		// so thinking is disabled at the template level for every request.
		TemplateKW: map[string]any{"enable_thinking": false},
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
	return out.Choices[0].Message, out.Usage, nil
}
