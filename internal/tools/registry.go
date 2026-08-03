package tools

import (
	"errors"

	"cerveau/internal/guard"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cerveau/internal/llm"
	"cerveau/internal/rfx"
)

const (
	RiskSafe      = "safe"
	RiskSensitive = "sensitive"
	RiskDangerous = "dangerous"
)

const (
	ModeDiscussion    = "discussion"
	ModeBrainstorming = "brainstorming"
	ModeAutopilot     = "autopilot"
)

type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// ModeTool is a Tool that wants the invoking mode at execution time. The
// Recipe Executor implements this so mode fencing propagates THROUGH a
// reflex into its re-dispatched steps (a bash step inside a reflex stays
// autopilot-only).
type ModeTool interface {
	Tool
	ExecuteMode(ctx context.Context, args json.RawMessage, mode string) (string, error)
}

type Entry struct {
	Tool       Tool
	RiskTier   string
	Modes      []string
	IngressCap int
	RetryClass string
}

type Guard func(tool string, args json.RawMessage) error

// Remediator transforms a tool call into its safe form (mv -> copy-verify-delete,
// back up an important file before edit, ...) and returns rewritten args. An error
// blocks the call. Runs AFTER the block-check, BEFORE execution.
type Remediator func(tool string, args json.RawMessage) (json.RawMessage, error)

type Registry struct {
	entries   map[string]Entry
	guard     Guard
	remediate Remediator
	postExec  func(name string, args json.RawMessage)
	workspace string
}

// SetWorkspace tells the registry its workspace root (exec-kind reflexes
// run their subprocesses there). Set at construction and on workspace switch.
func (r *Registry) SetWorkspace(ws string) { r.workspace = ws }

func NewRegistry(entries ...Entry) *Registry {
	r := &Registry{entries: map[string]Entry{}}
	for _, e := range entries {
		r.entries[e.Tool.Name()] = e
	}
	return r
}

func (r *Registry) SetGuard(g Guard) { r.guard = g }

func (r *Registry) SetRemediator(rm Remediator) { r.remediate = rm }

func (r *Registry) SetPostExec(f func(name string, args json.RawMessage)) { r.postExec = f }

func (r *Registry) Entry(name string) (Entry, bool) {
	e, ok := r.entries[name]
	return e, ok
}

// WithReflexes returns a session registry copy with all valid pipeline
// reflexes registered as NATIVE entries (declared risk/modes/cap — see
// AddReflexes). Fresh per turn, so a reflex added or edited on disk goes
// live on the NEXT turn: registry copy changes → GBNF rebuilt before the
// next Think, no restart. Collision errors are returned for loud surfacing.
func (r *Registry) WithReflexes(defs []rfx.Reflex) (*Registry, []error) {
	cp := &Registry{
		entries:   map[string]Entry{},
		guard:     r.guard,
		remediate: r.remediate,
		postExec:  r.postExec,
		workspace: r.workspace,
	}
	for k, e := range r.entries {
		cp.entries[k] = e
	}
	errs := cp.AddReflexes(defs)
	return cp, errs
}

func (r *Registry) Entries() []Entry {
	out := make([]Entry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	return out
}

// ReflexNames returns the names of registered RFX reflex tools available in
// the given mode ("" = any). Used to surface the reflex set in the system
// prompt — a tool the model doesn't know exists is a tool that doesn't.
func (r *Registry) ReflexNames(mode string) []string {
	var out []string
	for name, e := range r.entries {
		switch e.Tool.(type) {
		case *ReflexTool, *ExecReflexTool:
			if r.allowed(name, mode) {
				out = append(out, name)
			}
		}
	}
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (r *Registry) allowed(name, mode string) bool {
	e, ok := r.entries[name]
	if !ok {
		return false
	}
	if mode == "" || len(e.Modes) == 0 {
		return true
	}
	for _, m := range e.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

func (r *Registry) Specs(mode string) []llm.ToolSpec {
	out := []llm.ToolSpec{}
	for _, e := range r.entries {
		if !r.allowed(e.Tool.Name(), mode) {
			continue
		}
		out = append(out, llm.ToolSpec{
			Type: "function",
			Function: llm.FunctionSpec{
				Name:        e.Tool.Name(),
				Description: e.Tool.Description(),
				Parameters:  e.Tool.Schema(),
			},
		})
	}
	return out
}

func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	return r.ExecuteMode(ctx, name, args, "")
}

func (r *Registry) ExecuteMode(ctx context.Context, name string, args json.RawMessage, mode string) (string, error) {
	e, ok := r.entries[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	if mode != "" && !r.allowed(name, mode) {
		return "", fmt.Errorf("tool %q not available in %s mode", name, mode)
	}
	if mode == ModeDiscussion && (name == "edit" || name == "write") {
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(args, &a) == nil && !isDesignArtifact(a.Path) {
			return "", fmt.Errorf("discussion mode: writes limited to design artifacts (*.md, *.json, docs/) — code changes wait for Autopilot")
		}
	}
	if e.RiskTier == RiskDangerous && r.guard == nil {
		return "", fmt.Errorf("tool %q blocked: dangerous tier, Dispatch Guard not yet online (m1-guard)", name)
	}
	if r.guard != nil {
		if err := r.guard(name, args); err != nil {
			// A human-approved run (RFX_UI confirm strip / arm-click) passes
			// SENSITIVE denials — the approval IS the confirmation that tier
			// asks for. Catastrophic is never approvable, by anyone.
			var te *guard.TierError
			if !(HumanApproved(ctx) && errors.As(err, &te) && te.Tier == guard.TierSensitive) {
				return "", fmt.Errorf("guard denied %q: %w", name, err)
			}
		}
	}
	// Hard-rule remediation: rewrite the call to its safe form (or block if the
	// safe form can't be produced) BEFORE execution. Applies in every mode.
	if r.remediate != nil {
		rewritten, err := r.remediate(name, args)
		if err != nil {
			return "", fmt.Errorf("guard denied %q: %w", name, err)
		}
		args = rewritten
	}
	out, err := r.dispatch(ctx, e, name, args, mode)
	// Guidebook: mechanical failures (busy port, invalid regex) are repaired
	// and retried by the core itself — the model never burns an iteration on a
	// solved problem. Each repair is disclosed in the output. Real errors fall
	// through untouched.
	var fixNotes []string
	for attempt := 0; err != nil && attempt < maxAutoFixes; attempt++ {
		newArgs, note, ok := guidebookRepair(name, args, err.Error())
		if !ok {
			break
		}
		args = newArgs
		fixNotes = append(fixNotes, note)
		out, err = r.dispatch(ctx, e, name, args, mode)
	}
	if err == nil && len(fixNotes) > 0 {
		out = "[auto-fixed] " + strings.Join(fixNotes, "; ") + "\n" + out
	}
	if err == nil && r.postExec != nil {
		r.postExec(name, args)
	}
	return out, err
}

func (r *Registry) dispatch(ctx context.Context, e Entry, name string, args json.RawMessage, mode string) (string, error) {
	if mt, ok := e.Tool.(ModeTool); ok {
		return mt.ExecuteMode(ctx, args, mode)
	}
	return e.Tool.Execute(ctx, args)
}

// IngressCapFor returns the per-tool ingress cap (0 = uncapped). The loop uses
// this to bound what a tool result contributes to the WINDOW, while the full
// raw result is still written to episodic (source of truth, recallable).
func (r *Registry) IngressCapFor(name string) int {
	if e, ok := r.entries[name]; ok {
		return e.IngressCap
	}
	return 0
}

// CapIngress truncates a tool result for window use, leaving a pointer-style
// hint. Episodic keeps the untruncated original.
func CapIngress(out string, cap int) string {
	if cap > 0 && len(out) > cap {
		return out[:cap] + fmt.Sprintf("\n…[truncated at %d chars for the window — full result in the episodic log; re-run narrower for more]", cap)
	}
	return out
}

func isDesignArtifact(path string) bool {
	return strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".json") || strings.HasPrefix(path, "docs/")
}


// ── human approval (RFX_UI manual runs) ─────────────────────────────────

type humanApprovalKey struct{}

// WithHumanApproval marks ctx as carrying an explicit user confirmation
// (a confirm strip or arm/confirm click). It lets SENSITIVE guard denials
// pass in ExecuteMode; catastrophic never.
func WithHumanApproval(ctx context.Context) context.Context {
	return context.WithValue(ctx, humanApprovalKey{}, true)
}

func HumanApproved(ctx context.Context) bool {
	ok, _ := ctx.Value(humanApprovalKey{}).(bool)
	return ok
}
