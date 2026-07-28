package loop

import (
	"context"
	"strings"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
)

const (
	ErrClassTransient = "transient"
	ErrClassArgs      = "args"
	ErrClassMalformed = "malformed"
	ErrClassFatal     = "fatal"
)

var cardOptions = []string{"retry", "skip", "edit_args", "take_over"}

func errorCard(class, what, why, tried, fix string) map[string]any {
	return map[string]any{
		"class":        class,
		"what":         what,
		"why":          why,
		"tried":        tried,
		"options":      cardOptions,
		"proposed_fix": fix,
	}
}

func classifyLLMError(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "unreachable"),
		strings.Contains(s, "connection refused"),
		strings.Contains(s, "timeout"),
		strings.Contains(s, "deadline"),
		strings.Contains(s, "eof"),
		strings.Contains(s, "502"),
		strings.Contains(s, "503"),
		strings.Contains(s, "connection reset"):
		return ErrClassTransient
	default:
		return ErrClassFatal
	}
}

// isTruncatedToolCall spots llama.cpp's server-side failure to parse a tool
// call whose JSON arguments were cut off at the token cap — the generation hit
// max_tokens mid-string, so the JSON has no closing quote/brace. Recoverable:
// the model just needs to split the write into smaller calls.
func isTruncatedToolCall(err error) bool {
	s := err.Error()
	return strings.Contains(s, "Failed to parse tool call arguments") ||
		(strings.Contains(s, "json.exception.parse_error") && strings.Contains(s, "missing closing"))
}

func llmFix(class string) string {
	if class == ErrClassTransient {
		return "check the model endpoint is up, then retry"
	}
	return "inspect the error — likely a request/format issue"
}

func (l *Loop) completeWithRetry(ctx context.Context, wr *episodic.Writer, messages []llm.Message, specs []llm.ToolSpec, grammar string, proseCap int) (llm.Message, llm.Usage, error) {
	var lastErr error
	for attempt := 0; attempt <= 2; attempt++ {
		if attempt > 0 {
			wr.Append(episodic.Err, errorCard(
				ErrClassTransient,
				"model call failed — retrying",
				lastErr.Error(),
				strings.Repeat("·", attempt),
				llmFix(ErrClassTransient),
			))
			select {
			case <-ctx.Done():
				return llm.Message{}, llm.Usage{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * 1500 * time.Millisecond):
			}
		}
		msg, usage, err := l.llm.Complete(ctx, messages, specs, grammar, proseCap)
		if err == nil {
			return msg, usage, nil
		}
		lastErr = err
		if classifyLLMError(err) != ErrClassTransient {
			break
		}
	}
	return llm.Message{}, llm.Usage{}, lastErr
}
