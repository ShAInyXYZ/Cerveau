package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ApplyPatch applies several exact-string edits across files in ONE call.
// Rationale: edit is single-match, but real changes touch multiple files —
// and every extra tool call is a full LLM round-trip on a local model.
// Multi-edit is latency architecture, not convenience.
//
// Execution re-dispatches through the registry's edit/write tools, so
// backups, remediation, mode fencing, and the fs jail apply per hunk for
// free. ATOMIC: every old_string is pre-validated before any edit lands.
type ApplyPatch struct {
	reg *Registry
}

type patchHunk struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

func NewApplyPatch() *ApplyPatch { return &ApplyPatch{} }

// SetRegistry wires the dispatch target after registry construction
// (the registry can't exist before its own entries do).
func (t *ApplyPatch) SetRegistry(r *Registry) { t.reg = r }

func (t *ApplyPatch) Name() string { return "apply_patch" }

func (t *ApplyPatch) Description() string {
	return "Apply several edits at once: a list of {path, old_string, new_string}. All hunks are validated before ANY is applied (all-or-nothing). Empty old_string creates/overwrites the file. Prefer this over many edit calls."
}

func (t *ApplyPatch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"edits": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":       map[string]any{"type": "string"},
						"old_string": map[string]any{"type": "string"},
						"new_string": map[string]any{"type": "string"},
					},
					"required": []string{"path", "new_string"},
				},
			},
		},
		"required": []string{"edits"},
	}
}

const patchMaxHunks = 20

func (t *ApplyPatch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.ExecuteMode(ctx, args, "")
}

func (t *ApplyPatch) ExecuteMode(ctx context.Context, args json.RawMessage, mode string) (string, error) {
	if t.reg == nil {
		return "", fmt.Errorf("apply_patch: registry not wired")
	}
	var a struct {
		Edits []patchHunk `json:"edits"`
	}
	if err := json.Unmarshal(args, &a); err != nil || len(a.Edits) == 0 {
		return "", fmt.Errorf("edits: non-empty list required")
	}
	if len(a.Edits) > patchMaxHunks {
		return "", fmt.Errorf("%d hunks, max %d — split into multiple apply_patch calls", len(a.Edits), patchMaxHunks)
	}
	for i, h := range a.Edits {
		if h.Path == "" {
			return "", fmt.Errorf("hunk %d: path required", i+1)
		}
	}

	// Phase 1 — validate EVERY hunk before ANY edit lands (atomicity).
	// Read each target file via the registry (jail applies) and check the
	// old_string matches exactly once; empty old_string = create/overwrite.
	type validated struct {
		hunk   patchHunk
		tool   string // "edit" or "write"
		rawArg json.RawMessage
	}
	var plan []validated
	for i, h := range a.Edits {
		if h.OldString == "" {
			raw, _ := json.Marshal(map[string]string{"path": h.Path, "content": h.NewString})
			plan = append(plan, validated{h, "write", raw})
			continue
		}
		readRaw, _ := json.Marshal(map[string]string{"path": h.Path})
		content, err := t.reg.ExecuteMode(ctx, "read", readRaw, mode)
		if err != nil {
			return "", fmt.Errorf("hunk %d (%s): read failed: %w", i+1, h.Path, err)
		}
		// Validate with the SAME matcher edit applies (exact, or
		// indentation-only) so a hunk cannot pass validation and then fail on
		// apply, or vice versa.
		if n := matchCountFlexible(content, h.OldString); n != 1 {
			return "", fmt.Errorf("hunk %d (%s): old_string matches %d times (must match exactly once)%s — no edits applied", i+1, h.Path, n, nearestHint(content, h.OldString))
		}
		raw, _ := json.Marshal(map[string]string{"path": h.Path, "old_string": h.OldString, "new_string": h.NewString})
		plan = append(plan, validated{h, "edit", raw})
	}

	// Phase 2 — apply.
	var sb strings.Builder
	applied := 0
	for _, v := range plan {
		if _, err := t.reg.ExecuteMode(ctx, v.tool, v.rawArg, mode); err != nil {
			// Mid-apply failure: report honestly what landed and what didn't.
			fmt.Fprintf(&sb, "\n!! hunk (%s) FAILED after %d applied: %v", v.hunk.Path, applied, err)
			return sb.String(), fmt.Errorf("apply_patch: hunk %s failed after %d/%d applied: %w", v.hunk.Path, applied, len(plan), err)
		}
		fmt.Fprintf(&sb, "patched %s (%s)\n", v.hunk.Path, v.tool)
		applied++
	}
	fmt.Fprintf(&sb, "ok: %d/%d hunks applied", applied, len(plan))
	return sb.String(), nil
}
