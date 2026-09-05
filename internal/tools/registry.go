package tools

import (
	"bytes"
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

// registryKey carries the executing registry on the context, so a tool that
// dispatches to other tools (apply_patch → read/edit/write) runs them in the
// SAME jail. apply_patch used a registry pointer set once at startup; with
// per-session workspace registries that pointer was whichever workspace was
// wired last, and a Crane6 build patched files in the Crane folder
// (2026-09-04).
type registryKey struct{}

func WithRegistry(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, registryKey{}, r)
}

// sessionKey carries the session a tool call belongs to.
//
// It used to live only in one process-wide SessionContext that the chat
// handler overwrote at the start of every request. Two overlapping turns
// therefore shared it: a "restart that server" typed in one session while
// another was planning made that other session's commit_plan write its plan
// into the wrong log (2026-09-04). The loop now stamps each run's context
// with its own id, and the tools read that first.
type sessionKey struct{}

func WithSession(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionKey{}, id)
}

// SessionOf is the session a tool call belongs to: the one on the context,
// else the shared SessionContext's, else "".
func SessionOf(ctx context.Context, sctx *SessionContext) string {
	if v, _ := ctx.Value(sessionKey{}).(string); v != "" {
		return v
	}
	if sctx != nil {
		return sctx.SessionID
	}
	return ""
}

// RegistryFrom is the registry executing the current tool call, if any.
func RegistryFrom(ctx context.Context) *Registry {
	r, _ := ctx.Value(registryKey{}).(*Registry)
	return r
}

type modeKey struct{}

func ModeOf(ctx context.Context) string { m, _ := ctx.Value(modeKey{}).(string); return m }
func (r *Registry) ExecuteMode(ctx context.Context, name string, args json.RawMessage, mode string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Internal callers historically represent no parameters with a nil map,
	// which json.Marshal encodes as null. Normalize that shorthand here, before
	// guards and tools see it. Model-originated calls are validated as non-null
	// objects by the loop's executeCall boundary before reaching the registry.
	if args == nil || bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
		args = json.RawMessage(`{}`)
	}
	var object map[string]json.RawMessage
	if !json.Valid(args) || json.Unmarshal(args, &object) != nil || object == nil {
		return "", fmt.Errorf("invalid JSON arguments for %s", name)
	}
	ctx = context.WithValue(ctx, modeKey{}, mode)
	ctx = WithRegistry(ctx, r)
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
		original := append(json.RawMessage(nil), args...)
		rewritten, err := r.remediate(name, args)
		if err != nil {
			return "", fmt.Errorf("guard denied %q: %w", name, err)
		}
		if err := r.validateRemediatedArgs(ctx, name, original, rewritten, mode); err != nil {
			return "", err
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
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if r.guard != nil {
			if err := r.guard(name, args); err != nil {
				return out, fmt.Errorf("guard denied repaired call: %w", err)
			}
		}
		if r.remediate != nil {
			original := append(json.RawMessage(nil), args...)
			rewritten, repairErr := r.remediate(name, args)
			if repairErr != nil {
				return out, repairErr
			}
			if err := r.validateRemediatedArgs(ctx, name, original, rewritten, mode); err != nil {
				return out, err
			}
			args = rewritten
		}
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

// A remediator is a single transformation, not a second dispatch authority.
// Validate its output without invoking it recursively. Approval for the original
// arguments does not authorize a rewritten action that the guard would deny.
func (r *Registry) validateRemediatedArgs(ctx context.Context, name string, before, after json.RawMessage, mode string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(after, &object) != nil || object == nil {
		return fmt.Errorf("invalid remediated JSON arguments for %s", name)
	}
	if mode == ModeDiscussion && (name == "edit" || name == "write") {
		var path string
		if json.Unmarshal(object["path"], &path) != nil || !isDesignArtifact(path) {
			return fmt.Errorf("discussion mode: remediated writes limited to design artifacts")
		}
	}
	if !bytes.Equal(before, after) && r.guard != nil {
		if err := r.guard(name, after); err != nil {
			return fmt.Errorf("guard denied remediated call: %w", err)
		}
	}
	return nil
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
