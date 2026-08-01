// LEGACY PATH (sunset per M9): skill-declared tools use {{arg}} string
// templating into shell commands and are always registered RiskDangerous —
// the two flaws RFX was built to kill (typed params + declared risk).
// Kept working during transition; migrate skills with `crvcli rfx distill`,
// which produces a human-reviewed .rfx.yaml draft. New extensions: use RFX.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cerveau/internal/skills"
)

type skillTool struct {
	def   skills.SkillTool
	bash  *Bash
	guard Guard
}

func SkillTools(defs []skills.SkillTool, workspace string, guard Guard) []Tool {
	out := []Tool{}
	for _, d := range defs {
		out = append(out, &skillTool{def: d, bash: NewBash(workspace), guard: guard})
	}
	return out
}

func (t *skillTool) Name() string { return t.def.Name }

func (t *skillTool) Description() string { return t.def.Description }

func (t *skillTool) Schema() map[string]any {
	if t.def.Schema != nil {
		return t.def.Schema
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *skillTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	cmd := t.def.Command
	if cmd == "" {
		return "", fmt.Errorf("skill tool %q has no command", t.def.Name)
	}
	var flat map[string]any
	if len(args) > 0 {
		json.Unmarshal(args, &flat)
	}
	for k, v := range flat {
		s := fmt.Sprint(v)
		if strings.ContainsAny(s, "\x00") {
			return "", fmt.Errorf("bad arg %q", k)
		}
		cmd = strings.ReplaceAll(cmd, "{{"+k+"}}", s)
	}
	if strings.Contains(cmd, "{{") {
		return "", fmt.Errorf("unresolved template args in %q", cmd)
	}
	if t.guard != nil {
		guardArgs, _ := json.Marshal(map[string]string{"command": cmd})
		if err := t.guard("bash", guardArgs); err != nil {
			return "", fmt.Errorf("guard denied %q: %w", t.def.Name, err)
		}
	}
	runArgs, _ := json.Marshal(map[string]string{"command": cmd})
	return t.bash.Execute(ctx, runArgs)
}

func (r *Registry) WithSessionTools(ts []Tool) *Registry {
	cp := &Registry{
		entries:   map[string]Entry{},
		guard:     r.guard,
		remediate: r.remediate,
		postExec:  r.postExec,
	}
	for k, e := range r.entries {
		cp.entries[k] = e
	}
	for _, t := range ts {
		if _, exists := cp.entries[t.Name()]; exists {
			continue
		}
		cp.entries[t.Name()] = Entry{Tool: t, RiskTier: RiskDangerous, IngressCap: 8000, RetryClass: "transient"}
	}
	return cp
}
