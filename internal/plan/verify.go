package plan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// A step's verify is the observation that proves it done.
//
// Without one, "done" is inferred from whether the step's declared files exist
// — which cannot say which step wrote a file, and can never say a verification
// passed. That inference reported 4/4 green on the car run while the turn was
// dying in a check_page loop (2026-09-04).
//
// The model writes its own exam, so the SHAPE is constrained rather than the
// wording trusted. Two ways a self-written criterion goes wrong:
//
//   - Lazy: "index.html exists" passes trivially — the disk guess in a costume.
//   - Impossible: the car run's own eval could never pass under file:// however
//     correct the page was, and the model spent eight iterations on it.
//
// So a verify must be one of three RUNNABLE forms that can actually fail, and a
// step whose verify never passes gets a small budget before handing back: an
// unpassable check costs one step, never the task.
type Verify struct {
	// Kind is "eval", "command" or "contains". Exactly one of the matching
	// fields below is used.
	Kind string `json:"kind"`

	// eval: a check_page expression that must evaluate truthy. Path or URL
	// says where; a page with ES modules needs the URL form (file:// cannot
	// load modules — see check_page's own docs).
	Expr string `json:"expr,omitempty"`
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`

	// command: a shell command whose exit code decides it. Zero passes.
	Command string `json:"command,omitempty"`

	// contains: File must exist AND hold Symbol. Existence alone is not a
	// verify (see Validate) — the symbol is what makes it a real check.
	File   string `json:"file,omitempty"`
	Symbol string `json:"symbol,omitempty"`
}

// VerifyKinds are the accepted forms, for the schema and the error message.
var VerifyKinds = []string{"eval", "command", "contains"}

// reExistenceOnly matches the shapes a model reaches for when it wants a check
// that cannot fail: testing a path, or listing a file. Each has a real form the
// error points at, so this rejects the costume and not the intent.
var reExistenceOnly = regexp.MustCompile(`^\s*(\[\s*-[efsr]\s|test\s+-[efsr]\s|ls\s|stat\s|cat\s+[^|;&]*$|test\s+-[efsr])`)

// reConstantExpr matches an expression with no page in it: a literal, possibly
// negated, possibly wrapped in parens or a trailing semicolon.
var reConstantExpr = regexp.MustCompile(`(?i)^[\s(!]*(?:true|false|null|undefined|\d+(?:\.\d+)?|'[^']*'|"[^"]*")[\s);]*$`)

// isPage reports whether a workspace path is something a browser can render.
func isPage(p string) bool {
	l := strings.ToLower(strings.TrimSpace(p))
	return strings.HasSuffix(l, ".html") || strings.HasSuffix(l, ".htm")
}

// Validate reports why a verify is not a real check, or nil when it is one.
//
// Called at commit time, so a bad criterion is rejected the way the plan gate
// rejects a prose plan — before it can decide a step's fate.
func (v *Verify) Validate() error {
	if v == nil {
		return fmt.Errorf("no verify: every step needs one observation that proves it done")
	}
	switch strings.ToLower(strings.TrimSpace(v.Kind)) {
	case "eval":
		expr := strings.TrimSpace(v.Expr)
		if expr == "" {
			return fmt.Errorf("verify kind %q needs expr: the expression that must evaluate truthy", v.Kind)
		}
		// The first plan the model ever wrote checks for (2026-09-04) used
		// expr "true" on every step. A constant cannot fail, so it proves
		// nothing — it is the disk guess again, dressed as an eval.
		if reConstantExpr.MatchString(expr) {
			return fmt.Errorf("verify expr %q is a constant — it can never fail, so it proves nothing. "+
				"Read real page state: !!document.querySelector('canvas'), window.__state.speed > 0, typeof buildFan === 'function'", expr)
		}
		path, url := strings.TrimSpace(v.Path), strings.TrimSpace(v.URL)
		if path == "" && url == "" {
			return fmt.Errorf("verify kind %q needs path or url: which page to evaluate it in", v.Kind)
		}
		// check_page loads a PAGE. Pointed at a .js file it renders nothing
		// and every expression is false — the same first plan put eval on
		// src/core/constants.js.
		if path != "" && !isPage(path) {
			return fmt.Errorf("verify kind eval needs an HTML page, got %q — check_page loads a page, not a script. "+
				"For a .js file use kind \"contains\" (a symbol it must define) or \"command\" (node --check %s)", path, path)
		}
		return nil
	case "command":
		c := strings.TrimSpace(v.Command)
		if c == "" {
			return fmt.Errorf("verify kind %q needs command: a command whose exit code decides the step", v.Kind)
		}
		if reExistenceOnly.MatchString(c) {
			return fmt.Errorf("verify %q only checks that a file exists, which is true the moment anything writes it — "+
				"that is the disk guess, not a check. Use a command that can FAIL on wrong content "+
				"(a test run, a linter, node --check), or kind \"contains\" with the symbol the step must add", c)
		}
		return nil
	case "contains":
		if strings.TrimSpace(v.File) == "" {
			return fmt.Errorf("verify kind %q needs file", v.Kind)
		}
		if strings.TrimSpace(v.Symbol) == "" {
			return fmt.Errorf("verify kind %q needs symbol: the name this step must add to %s. "+
				"A file that merely exists proves nothing about which step wrote it", v.Kind, v.File)
		}
		return nil
	case "":
		return fmt.Errorf("no verify kind: use one of %s", strings.Join(VerifyKinds, ", "))
	default:
		return fmt.Errorf("unknown verify kind %q: use one of %s", v.Kind, strings.Join(VerifyKinds, ", "))
	}
}

// Describe is the one-line human form, for prompts and the step report.
func (v *Verify) Describe() string {
	if v == nil {
		return ""
	}
	switch strings.ToLower(v.Kind) {
	case "eval":
		where := v.Path
		if where == "" {
			where = v.URL
		}
		return fmt.Sprintf("check_page eval on %s must be truthy: %s", where, oneLine(v.Expr))
	case "command":
		return fmt.Sprintf("`%s` must exit 0", oneLine(v.Command))
	case "contains":
		return fmt.Sprintf("%s must contain %q", v.File, v.Symbol)
	}
	return ""
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}

// VerifySchema is the JSON-schema fragment for one step's verify, shared by
// commit_plan so the model sees the same contract that Validate enforces.
func VerifySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"description": "REQUIRED. The observation that proves this step done — a check that can FAIL. " +
			"Not \"the file exists\": that is true the moment anything writes it.",
		"properties": map[string]any{
			"kind": map[string]any{
				"type": "string", "enum": VerifyKinds,
				"description": "eval: a check_page expression that must be truthy. command: exit code 0. contains: a file must hold a named symbol.",
			},
			"expr":    map[string]any{"type": "string", "description": "eval: the expression, e.g. \"!!document.querySelector('canvas') && window.__state.speed > 0\""},
			"path":    map[string]any{"type": "string", "description": "eval: workspace file to evaluate in"},
			"url":     map[string]any{"type": "string", "description": "eval: served URL instead of path — REQUIRED if the page uses ES modules, which cannot load over file://"},
			"command": map[string]any{"type": "string", "description": "command: e.g. \"node --check app.js\" or a test run. Must be able to fail on wrong content."},
			"file":    map[string]any{"type": "string", "description": "contains: the file"},
			"symbol":  map[string]any{"type": "string", "description": "contains: the name this step must add, e.g. \"buildFan\""},
		},
		"required": []string{"kind"},
	}
}

// UnmarshalVerify parses a verify from a step's raw JSON, tolerating absence.
func UnmarshalVerify(raw json.RawMessage) (*Verify, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var v Verify
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	return &v, nil
}
