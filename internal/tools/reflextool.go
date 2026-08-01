package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"cerveau/internal/rfx"
)

// ReflexTool turns a validated rfx.Reflex into a registry Tool (spec §3).
// Execution re-dispatches every step THROUGH the registry, so the guard,
// remediator, ingress caps, and episodic logging apply per step — the
// executor adds no new trust surface, only composition.
type ReflexTool struct {
	def rfx.Reflex
	reg *Registry
}

func (t *ReflexTool) Name() string        { return t.def.Name }
func (t *ReflexTool) Description() string { return t.def.Description }

func (t *ReflexTool) Schema() map[string]any {
	if t.def.Params != nil {
		return t.def.Params
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *ReflexTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.ExecuteMode(ctx, args, "")
}

// reflexDepthKey bounds reflexes-calling-reflexes (a step may name another
// reflex). No cycles detection needed — a hard depth cap catches A→B→A too.
type reflexDepthKey struct{}

const maxReflexDepth = 3

func (t *ReflexTool) ExecuteMode(ctx context.Context, args json.RawMessage, mode string) (string, error) {
	depth, _ := ctx.Value(reflexDepthKey{}).(int)
	if depth >= maxReflexDepth {
		return "", fmt.Errorf("reflex %s: max nesting depth %d reached (reflexes calling reflexes)", t.def.Name, maxReflexDepth)
	}
	ctx = context.WithValue(ctx, reflexDepthKey{}, depth+1)

	params := map[string]any{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return "", fmt.Errorf("reflex %s: bad args JSON: %w", t.def.Name, err)
		}
	}
	if err := validateReflexArgs(t.def.Params, params); err != nil {
		return "", fmt.Errorf("reflex %s: %w", t.def.Name, err)
	}

	outputs := map[string]string{} // step id → output (successful steps only)
	status := map[string]bool{}    // step id → ok
	var report strings.Builder

	for i, s := range t.def.Steps {
		label := stepLabel(i, s)
		if s.When != "" {
			run, err := rfx.EvalWhen(s.When, func(id string) (bool, bool) {
				ok, defined := status[id]
				return ok, defined
			})
			if err != nil {
				return report.String(), fmt.Errorf("reflex %s step %s: %w", t.def.Name, label, err)
			}
			if !run {
				fmt.Fprintf(&report, "== %s (%s) [skipped: when %s] ==\n", label, s.Tool, s.When)
				continue
			}
		}

		stepArgs, err := substituteStepArgs(s, params, outputs)
		if err != nil {
			return report.String(), fmt.Errorf("reflex %s step %s: %w", t.def.Name, label, err)
		}
		// Capability card enforcement — in Go, per dispatch (spec §5).
		if argMap, ok := stepArgs.(map[string]any); ok {
			if err := rfx.CheckStep(t.def.Card, s.Tool, argMap); err != nil {
				return report.String(), fmt.Errorf("reflex %s step %s: %w", t.def.Name, label, err)
			}
		}
		raw, err := json.Marshal(stepArgs)
		if err != nil {
			return report.String(), fmt.Errorf("reflex %s step %s: args don't serialize: %w", t.def.Name, label, err)
		}

		out, err := t.reg.ExecuteMode(ctx, s.Tool, raw, mode)
		ok := err == nil
		if s.ID != "" {
			status[s.ID] = ok
			if ok {
				outputs[s.ID] = out
			}
		}

		if !ok {
			// Real output is KEPT and returned, so the model self-corrects —
			// same philosophy as core bash (never a bare "exit status 1").
			fmt.Fprintf(&report, "== %s (%s) [FAILED%s] ==\n%s\n", label, s.Tool, optSuffix(s), out)
			if s.Optional {
				continue
			}
			return report.String(), fmt.Errorf("reflex %s step %s (%s) failed: %w", t.def.Name, label, s.Tool, err)
		}
		fmt.Fprintf(&report, "== %s (%s) [ok] ==\n%s\n", label, s.Tool, out)
	}
	return report.String(), nil
}

func stepLabel(i int, s rfx.Step) string {
	if s.ID != "" {
		return s.ID
	}
	return fmt.Sprintf("step %d", i+1)
}

func optSuffix(s rfx.Step) string {
	if s.Optional {
		return ", optional — continuing"
	}
	return ""
}

// AddReflexes registers validated reflexes as NATIVE entries carrying their
// declared risk tier, modes, and ingress cap (spec §7). Pipeline reflexes
// become ReflexTools; exec reflexes become Synapse ExecReflexTools running
// in the registry's workspace. Collisions with existing tools are rejected
// loudly, never overridden.
func (r *Registry) AddReflexes(defs []rfx.Reflex) []error {
	var errs []error
	for _, d := range defs {
		if _, exists := r.entries[d.Name]; exists {
			errs = append(errs, fmt.Errorf("reflex %q collides with an existing tool — rejected (spec §2)", d.Name))
			continue
		}
		var tool Tool
		switch d.Kind {
		case rfx.KindPipeline:
			tool = &ReflexTool{def: d, reg: r}
		case rfx.KindExec:
			if r.workspace == "" {
				errs = append(errs, fmt.Errorf("exec reflex %q needs a registry workspace — none set", d.Name))
				continue
			}
			tool = &ExecReflexTool{def: d, dir: r.workspace}
		default:
			continue // loader guarantees kind; defensive skip
		}
		r.entries[d.Name] = Entry{
			Tool:       tool,
			RiskTier:   d.Risk,
			Modes:      d.Modes, // nil = all modes, registry semantics
			IngressCap: d.Cap(),
			RetryClass: "args",
		}
	}
	return errs
}

// ── substitution (spec §3.2: typed, quoted, never stringly) ─────────────

var reflexPhRe = regexp.MustCompile(`\{\{\s*(params\.[A-Za-z0-9_-]+|steps\.[a-z0-9-]+\.output)\s*\}\}`)

// substituteStepArgs resolves placeholders in one step's args. bash strings
// are shell contexts: embedded substitutions are POSIX-quoted. A field that
// is exactly one placeholder gets the RAW typed value.
func substituteStepArgs(s rfx.Step, params map[string]any, outputs map[string]string) (any, error) {
	if s.Tool == "bash" {
		str, _ := s.Args.(string)
		out, err := substituteString(str, params, outputs, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": out}, nil
	}
	return substituteValue(s.Args, params, outputs, false)
}

func substituteValue(v any, params map[string]any, outputs map[string]string, shell bool) (any, error) {
	switch t := v.(type) {
	case string:
		// Whole-field placeholder → raw typed value (number stays number).
		if loc := reflexPhRe.FindStringIndex(t); loc != nil && loc[0] == 0 && loc[1] == len(t) {
			return resolvePlaceholder(reflexPhRe.FindStringSubmatch(t)[1], params, outputs)
		}
		return substituteString(t, params, outputs, shell)
	case map[string]any:
		out := map[string]any{}
		for k, vv := range t {
			sv, err := substituteValue(vv, params, outputs, shell)
			if err != nil {
				return nil, err
			}
			out[k] = sv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			sv, err := substituteValue(vv, params, outputs, shell)
			if err != nil {
				return nil, err
			}
			out[i] = sv
		}
		return out, nil
	default:
		return v, nil
	}
}

func substituteString(s string, params map[string]any, outputs map[string]string, shell bool) (string, error) {
	var err error
	out := reflexPhRe.ReplaceAllStringFunc(s, func(ph string) string {
		if err != nil {
			return ""
		}
		ref := reflexPhRe.FindStringSubmatch(ph)[1]
		val, e := resolvePlaceholder(ref, params, outputs)
		if e != nil {
			err = e
			return ""
		}
		str := stringify(val)
		if shell {
			return shQuote(str)
		}
		return str
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

func resolvePlaceholder(ref string, params map[string]any, outputs map[string]string) (any, error) {
	ref = strings.Join(strings.Fields(ref), "")
	if strings.HasPrefix(ref, "params.") {
		name := strings.TrimPrefix(ref, "params.")
		val, ok := params[name]
		if !ok {
			return nil, fmt.Errorf("param %q not provided", name)
		}
		return val, nil
	}
	id := strings.TrimSuffix(strings.TrimPrefix(ref, "steps."), ".output")
	out, ok := outputs[id]
	if !ok {
		return nil, fmt.Errorf("step %q has no output (failed or was skipped)", id)
	}
	return out, nil
}

// shQuote POSIX-quotes a value for safe embedding in a shell command:
// ' → '\” wrapped in single quotes. An arg of `"; rm -rf ~;"` becomes inert.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// validateReflexArgs re-checks args against the params schema at execution.
// The GBNF grammar constrains model-generated calls at decode; this catches
// direct API / crvcli calls that bypass the grammar.
func validateReflexArgs(schema map[string]any, args map[string]any) error {
	if schema == nil {
		if len(args) > 0 {
			return fmt.Errorf("takes no params, got %d", len(args))
		}
		return nil
	}
	props, _ := schema["properties"].(map[string]any)
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			name, _ := r.(string)
			if _, present := args[name]; !present {
				return fmt.Errorf("missing required param %q", name)
			}
		}
	}
	for name, val := range args {
		sub, known := props[name]
		if !known {
			return fmt.Errorf("unknown param %q", name)
		}
		sm, _ := sub.(map[string]any)
		if err := checkArgType(name, sm, val); err != nil {
			return err
		}
	}
	return nil
}

func checkArgType(name string, schema map[string]any, val any) error {
	if enums, ok := schema["enum"].([]any); ok {
		str, _ := val.(string)
		for _, e := range enums {
			if es, _ := e.(string); es == str {
				return nil
			}
		}
		return fmt.Errorf("param %q: %v not in enum", name, val)
	}
	switch typ, _ := schema["type"].(string); typ {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("param %q: must be a string", name)
		}
	case "integer":
		f, ok := val.(float64)
		if !ok || f != float64(int64(f)) {
			return fmt.Errorf("param %q: must be an integer", name)
		}
	case "number":
		if _, ok := val.(float64); !ok {
			return fmt.Errorf("param %q: must be a number", name)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("param %q: must be a boolean", name)
		}
	case "array":
		if _, ok := val.([]any); !ok {
			return fmt.Errorf("param %q: must be an array", name)
		}
	case "object":
		if _, ok := val.(map[string]any); !ok {
			return fmt.Errorf("param %q: must be an object", name)
		}
	}
	return nil
}
