package llm

import "context"

// Thinking levels are the model's own: the Qwen3.8 chat template accepts
// enable_thinking and reasoning_effort in {low, medium, xhigh}. "off" is ours.
const (
	ThinkingOff    = "off"
	ThinkingLow    = "low"
	ThinkingMedium = "medium"
	ThinkingXHigh  = "xhigh"
)

// ThinkingBudget is added to a call's max_tokens when thinking is on, so the
// reasoning has room and the answer still fits after it. Reasoning is not
// re-sent, so this costs decode time, not window. Sized per level: medium
// thought 13k tokens on one planning call (2026-09-04) and was cut off at 8k.
const ThinkingBudget = 8192

func BudgetFor(level string) int {
	switch level {
	case ThinkingLow:
		return 4096
	case ThinkingMedium:
		return 16384
	case ThinkingXHigh:
		return 32768
	}
	return 0
}

// StepDown is the next cheaper level: what a turn falls back to when the
// reasoning at the current level ran past its budget without an answer.
func StepDown(level string) string {
	switch level {
	case ThinkingXHigh:
		return ThinkingMedium
	case ThinkingMedium:
		return ThinkingLow
	}
	return ThinkingOff
}

type budgetKey struct{}

// WithThinkingBudget overrides the per-call reasoning budget under ctx — the
// planning call gets more room than a step, because dividing the task is
// where deep thinking pays and it happens once.
func WithThinkingBudget(ctx context.Context, tokens int) context.Context {
	return context.WithValue(ctx, budgetKey{}, tokens)
}

func ThinkingBudgetOf(ctx context.Context) int {
	v, _ := ctx.Value(budgetKey{}).(int)
	return v
}

func ThinkingLevels() []string {
	return []string{ThinkingOff, ThinkingLow, ThinkingMedium, ThinkingXHigh}
}

func ValidThinking(level string) bool {
	for _, l := range ThinkingLevels() {
		if l == level {
			return true
		}
	}
	return false
}

// SetThinking sets the session default level. Invalid input means off.
func (c *Client) SetThinking(level string) {
	if !ValidThinking(level) {
		level = ThinkingOff
	}
	c.thinking = level
}

func (c *Client) ThinkingLevel() string {
	if c.thinking == "" {
		return ThinkingOff
	}
	return c.thinking
}

type thinkingKey struct{}

// WithThinking sets the thinking level for the calls made under ctx: a turn
// running in autopilot, or a helper call that must never think.
func WithThinking(ctx context.Context, level string) context.Context {
	return context.WithValue(ctx, thinkingKey{}, level)
}

func ThinkingOf(ctx context.Context) string {
	v, _ := ctx.Value(thinkingKey{}).(string)
	return v
}

type forcedToolKey struct{}

// WithForcedTool makes the calls under ctx REQUIRE a call to the named tool.
//
// Sent as OpenAI tool_choice {"type":"function","function":{"name":…}}, which
// vLLM honours with guided decoding: the reply IS a well-formed call to that
// tool, arguments constrained to its schema. Two failures vanish at once. The
// model cannot wander off to a tool it remembers but was not offered — with
// commit_plan as its only option it still called glob and read, every time —
// and the qwen3_xml parser cannot swallow a hand-rolled call it could not
// parse, because the arguments are generated inside the schema.
func WithForcedTool(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, forcedToolKey{}, name)
}

func ForcedToolOf(ctx context.Context) string {
	v, _ := ctx.Value(forcedToolKey{}).(string)
	return v
}
