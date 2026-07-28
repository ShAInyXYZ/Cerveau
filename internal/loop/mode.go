package loop

type Mode struct {
	Name     string
	Module   string
	ProseCap int
	MaxIter  int
}

const basePrompt = "You are Cerveau, a local agentic harness. Be concise and direct. Use tools when they help; answer plainly when done."

var Modes = map[string]Mode{
	"discussion": {
		Name: "discussion",
		Module: "MODE: Discussion — ultra-concise planning dialogue. Short, direct answers; no essays, no filler. " +
			"Build the plan step by step with the user. Writes are limited to design artifacts. " +
			"When the plan is agreed, crystallize it with the commit_plan tool.",
		ProseCap: 600,
		MaxIter:  8,
	},
	"autopilot": {
		Name: "autopilot",
		Module: "MODE: Autopilot — full autonomy. Carry the task through end to end without asking for " +
			"step-by-step approval. Plan, act, verify, and when reality diverges from expectation " +
			"(a build fails, a dependency is incompatible, an approach doesn't work) diagnose and " +
			"RE-PLAN on the fly rather than stopping. Keep going until the task is genuinely done. " +
			"Hand back only when truly blocked, when you'd need information only the user has, or when " +
			"repeated attempts fail. Narrate minimally; let the work speak. " +
			"Safety is enforced structurally by the harness (destructive ops are blocked or auto-made-safe), " +
			"so act decisively within that floor. " +
			"If a committed plan is present below, treat it as your guide — follow its intent, but adapt freely.",
		// Autopilot WRITES WHOLE FILES in tool calls — the cap must fit them. At
		// 2048 a ~8KB game.js got truncated mid-JSON and llama.cpp's tool parser
		// choked ("missing closing quote" at col ~8122 = 2048 tok × 4 chars).
		ProseCap: 8192,
		MaxIter:  40,
	},
	"brainstorming": {
		Name: "brainstorming",
		Module: "MODE: Brainstorming — deep research and long-term planning, email-style. " +
			"Dense, thorough, concrete responses. Research BEFORE answering: web_fetch, code tools, and your recalled memory. " +
			"Externalize large findings into research notes with the write tool (notes/ directory) and reference them — never dump raw content into the conversation.",
		ProseCap: 8192, // writes whole research-note files — same per-call headroom as autopilot
		MaxIter:  16,
	},
}

func ModeByName(name string) Mode {
	if m, ok := Modes[name]; ok {
		return m
	}
	return Modes["discussion"]
}
