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

const (
	// max(10k, 5% of window) of headroom between the counted prompt and the
	// output cap — see the clamp below.
	outputClampMargin = 10000
	// never clamp below this: a tiny cap produces truncated, useless replies.
	minClampedOutput = 1024
)

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
		// Fit the output cap inside what is left of the context window.
		//
		// ProseCap is a fixed 8192 in autopilot. llama.cpp treats an
		// over-large max_tokens as advisory and just truncates; vLLM REJECTS
		// the request outright:
		//   "requested 8192 output tokens and your prompt contains at least
		//    24577 input tokens, for a total of at least 32769 tokens"
		// A Core must not need the harness to know which engine it is, so cap
		// here against the live prompt size instead of assuming leniency.
		// Margin follows qwen-code's clampOutputTokensToWindow: max(10k, 5% of
		// the window). 256 was not enough — our counter said 24581 where vLLM
		// counted 24577, and an under-count is the ONE way prompt+max_tokens
		// can still overflow. Floor the room FIRST, then cap by the ceiling,
		// so a deliberately low ceiling is respected rather than inflated.
		cap := proseCap
		if l.win != nil {
			margin := l.win.Budget() / 20
			if margin < outputClampMargin {
				margin = outputClampMargin
			}
			room := l.win.Budget() - l.win.CountRequest(messages, specs) - margin
			// If room is negative the prompt itself is over budget. Raising it
			// back to a floor would send a request we KNOW overflows — that is
			// the 502 this clamp exists to prevent. Ask for the minimum and let
			// the packer's next trim reclaim space.
			if room < minClampedOutput {
				room = minClampedOutput
			}
			if room < cap {
				cap = room
			}
		}
		msg, usage, err := l.llm.Complete(ctx, messages, specs, grammar, cap)
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

// repeatHint coaches a model that has called the same tool twice and got the
// same answer both times. Naming the specific escape route matters more than
// "try something else" — the read-truncation loop is the common case.
func repeatHint(tool, lastOutput string) string {
	if tool == "read" && strings.Contains(lastOutput, "call read again with offset") {
		return "You just read the same slice twice. The file is longer than one read: " +
			"call read again with the OFFSET given in the notice to get the next slice, " +
			"or use grep/find_symbol to jump straight to what you need."
	}
	return genericRepeatHint(tool)
}

func genericRepeatHint(tool string) string {
	return "That exact " + tool + " call returned the same result twice — repeating it will not help. " +
		"Change the arguments, use a different tool, or act on what you already have."
}

// manglingPipes hide the output a model is trying to read. od/xxd/hexdump turn
// an error message into bytes; head/tail/sed with a tiny range cut it off.
var manglingPipes = []string{"od ", "od -", "xxd", "hexdump"}

// repeatHintFor is repeatHint plus the command that produced the output, so a
// repeat can be diagnosed from what the model actually ran.
//
// The case this exists for: the model debugged a real bug by piping the failing
// command through `od -c | sed -n '1,1p'`, which reduced the error to one line
// of hex. It could not read the error, so it re-ran the same call eight times
// and the turn died on guard_loop_detected. Telling it to "change the
// arguments" is useless — it cannot see what it destroyed. Name the pipe.
func repeatHintFor(tool, lastOutput, command string) string {
	if tool != "bash" || command == "" {
		return repeatHint(tool, lastOutput)
	}
	for _, p := range manglingPipes {
		if i := strings.Index(command, "| "+p); i >= 0 {
			name := strings.TrimSpace(strings.TrimSuffix(strings.Fields(command[i+2:])[0], "-"))
			return "That exact bash call returned the same result twice. You piped it through `" +
				name + "`, which turned the output into bytes — the real message is not in " +
				"what you are reading. Re-run the command WITHOUT the `" + name + "` pipe " +
				"(and without any head/sed truncation) so you can see the actual error."
		}
	}
	// truncation of something that looks like an error
	if looksTruncatedError(lastOutput) && hasTruncatingPipe(command) {
		return "That exact bash call returned the same result twice, and the output is a " +
			"TRUNCATED error — your pipe cut it off before the useful part. Re-run it " +
			"without the head/tail/sed truncation to see the full error text."
	}
	return repeatHint(tool, lastOutput)
}

// repeatHintArgs is repeatHintFor for callers holding raw JSON tool arguments.
func repeatHintArgs(tool, lastOutput string, args []byte) string {
	var a struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(args, &a)
	return repeatHintFor(tool, lastOutput, a.Command)
}

func hasTruncatingPipe(cmd string) bool {
	for _, p := range []string{"| head", "|head", "| tail", "|tail", "| sed -n", "| cut "} {
		if strings.Contains(cmd, p) {
			return true
		}
	}
	return false
}

func looksTruncatedError(out string) bool {
	for _, marker := range []string{"Traceback", "Error", "error:", "Exception", "panic:", "[eval]"} {
		if strings.Contains(out, marker) {
			return true
		}
	}
	return false
}
