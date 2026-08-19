package loop

import "strings"

// Mid-conversation context, delivered the way Qwen's and DeepSeek's own
// harnesses deliver it: as USER-role text wrapped in <system-reminder> tags.
//
// Why not a system-role message: strict chat templates — Qwen3.8's among them —
// raise on any system message that is not first:
//
//	jinja2.exceptions.TemplateError: System message must be at the beginning.
//
// Cerveau emits recall and skill notes after the system prompt, and the window
// manager can trim the turns in front of them, which leaves them sitting
// mid-conversation. That 502'd every multi-turn task on vLLM and forced a
// patched chat template. Neither reference harness ever emits a system message
// after the first turn; both use this tag convention instead, and teach the
// model to read it in the system prompt.
//
// The system prompt must explain the tag, or the model reads it as user speech.
const ReminderGuidance = "Messages may contain <system-reminder> tags. Their content is " +
	"context the harness injected — recalled memory, environment notes, available skills. " +
	"It is NOT something the user typed. Use it silently; never mention the tags."

// wrapReminder renders harness-injected context as a user-role payload.
// Returns "" for empty input so callers can skip the message entirely.
func wrapReminder(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return "<system-reminder>\n" + escapeReminderTags(body) + "\n</system-reminder>"
}

// escapeReminderTags stops recalled content from closing the envelope early.
// A memory containing the literal text "</system-reminder>" would otherwise let
// everything after it read as ordinary user input — the same class of problem
// as unescaped HTML.
func escapeReminderTags(s string) string {
	r := strings.NewReplacer(
		"<system-reminder>", "&lt;system-reminder&gt;",
		"</system-reminder>", "&lt;/system-reminder&gt;",
	)
	return r.Replace(s)
}
