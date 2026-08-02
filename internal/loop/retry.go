package loop

import (
	"context"
	"encoding/json"
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

// looksTruncated reports whether tool-call arguments are cut-off JSON rather
// than merely wrong JSON: the generation hit its token cap mid-value, so
// quotes/braces never close. Distinguishing the two matters — one is fixed by
// splitting the write, the other by re-reading the schema.
func looksTruncated(args string) bool {
	s := strings.TrimSpace(args)
	if s == "" || !strings.HasPrefix(s, "{") {
		return false
	}
	if json.Valid([]byte(s)) {
		return false
	}
	inStr, esc, depth := false, false, 0
	for _, r := range s {
		switch {
		case esc:
			esc = false
		case r == '\\' && inStr:
			esc = true
		case r == '"':
			inStr = !inStr
		case inStr:
			// inside a string: braces don't count
		case r == '{', r == '[':
			depth++
		case r == '}', r == ']':
			depth--
		}
	}
	// unterminated string, or containers still open at EOF = cut off
	return inStr || depth > 0
}

// malformedHint turns invalid tool-call arguments into advice the model can
// act on. Truncation is the common local-model failure: the content was too
// big for one call.
func malformedHint(args string) string {
	if looksTruncated(args) {
		return "your arguments were CUT OFF at the output limit — the JSON never closed. " +
			"Do NOT resend the same call: split the work into smaller calls " +
			"(write part of the file, then append the rest with edit), or write a shorter file."
	}
	return "arguments are not valid JSON — regenerate them following the tool's schema exactly"
}

// splitCorrection is the self-correction fed back when a tool call is cut off
// at the token cap. It must name the CONTINUATION path (append with edit),
// not only "write less" — a file that overflowed cannot be rewritten whole.
func splitCorrection(tool string) string {
	return "Your last " + tool + " call was CUT OFF mid-generation: its arguments exceeded the output limit. " +
		"Do NOT resend the same call — it will be cut off again. Instead: " +
		"(1) write a FIRST, SHORTER chunk of the file with `write`, then " +
		"(2) APPEND each remaining chunk with `edit` (old_string = the last line you wrote, " +
		"new_string = that line plus the next chunk), or " +
		"(3) split the content across several smaller files. " +
		"Keep every single call well under 4000 tokens of content."
}
