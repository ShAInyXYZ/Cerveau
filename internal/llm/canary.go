package llm

import (
	"context"
	"fmt"
)

// ToolCanary asks the model to make one trivial tool call and reports whether
// the server returned it STRUCTURED.
//
// Why this exists: vLLM needs --enable-auto-tool-choice and the right
// --tool-call-parser for the model's template (qwen3_xml for Qwen3.8's
// <tool_call><function=…> format; hermes and qwen3_coder are the other
// candidates). Pick the wrong one and nothing errors — the call arrives as
// PLAIN TEXT in content, the harness sees no tool_calls, and the agent looks
// like it simply refuses to use tools. llama.cpp needs no parser at all, so
// this failure only appears when a Core is swapped.
//
// A misconfigured parser is silent; a canary makes it loud.
func (c *Client) ToolCanary(ctx context.Context) error {
	spec := ToolSpec{Type: "function"}
	spec.Function.Name = "canary"
	spec.Function.Description = "Report readiness. Call this with ok=true."
	spec.Function.Parameters = map[string]any{
		"type":       "object",
		"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
		"required":   []string{"ok"},
	}

	msg, _, err := c.Complete(ctx,
		[]Message{{Role: "user", Content: "Call the canary tool with ok=true. Reply with the tool call only."}},
		[]ToolSpec{spec}, "", 128)
	if err != nil {
		return fmt.Errorf("canary request failed: %w", err)
	}
	if len(msg.ToolCalls) == 0 {
		return fmt.Errorf("model returned no structured tool_calls — the server's "+
			"tool-call parser is likely wrong or --enable-auto-tool-choice is missing "+
			"(content was: %.80q)", msg.Content)
	}
	return nil
}
