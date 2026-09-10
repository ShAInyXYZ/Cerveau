package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ApplyPatch applies several exact-string edits across files in ONE call.
// Rationale: edit is single-match, but real changes touch multiple files —
// and every extra tool call is a full LLM round-trip on a local model.
// Multi-edit is latency architecture, not convenience.
//
// Execution re-dispatches through the registry's edit/write tools, so
// backups, remediation, mode fencing, and the fs jail apply per hunk for
// free. Every old_string is pre-validated before any edit lands. This is not
// a filesystem transaction: a later I/O/guard failure reports partial progress.
type ApplyPatch struct {
	reg *Registry
}

type patchHunk struct {
	Path        string  `json:"path"`
	OldString   string  `json:"old_string"`
	NewString   string  `json:"new_string"`
	ExpectedSHA *string `json:"expected_sha256,omitempty"`
}

func NewApplyPatch() *ApplyPatch { return &ApplyPatch{} }

// SetRegistry wires the dispatch target after registry construction
// (the registry can't exist before its own entries do).
func (t *ApplyPatch) SetRegistry(r *Registry) { t.reg = r }

func (t *ApplyPatch) Name() string { return "apply_patch" }

func (t *ApplyPatch) Description() string {
	return "Apply several edits with {edits:[{path,old_string,new_string,expected_sha256?}]}. EVERY hunk requires its own workspace-relative path and both strings. Copy source without read's line-number prefixes. Optional expected_sha256 refers to the file immediately BEFORE that hunk, including preceding hunks. Hunks and final JS syntax are validated before any is applied; intermediate hunks may be incomplete when the complete batch is valid. Existing broken JS remains repairable with explicit syntax status; other languages are not syntax checked. Runtime write failures report partial progress. Explicit empty old_string creates/overwrites a file; explicit empty new_string deletes the match. Keep hunks small and uniquely matched."
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
						"path":            map[string]any{"type": "string", "description": "Workspace-relative target; required in EACH edit, never at the top level."},
						"old_string":      map[string]any{"type": "string", "description": "Unique current source text without read's line-number prefixes. Explicit empty string creates/overwrites the file."},
						"new_string":      map[string]any{"type": "string", "description": "Replacement source text; explicit empty string deletes the match."},
						"expected_sha256": map[string]any{"type": "string", "description": "Optional expected full-file SHA-256 immediately before this hunk; subsequent hunks on the same file see the preceding hunks' projected result."},
					},
					"required": []string{"path", "old_string", "new_string"},
				},
			},
		},
		"required": []string{"edits"},
	}
}

const patchMaxHunks = 20

const patchShapeHint = ` — no edits applied. Use {"edits":[{"path":"relative/file","old_string":"exact current source","new_string":"replacement"}]}; put path in EVERY edit, not at the top level. Both strings are required; use an explicit empty string only for create/overwrite or deletion.`

func (t *ApplyPatch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.ExecuteMode(ctx, args, "")
}

func (t *ApplyPatch) ExecuteMode(ctx context.Context, args json.RawMessage, mode string) (string, error) {
	// Dispatch through the registry that is running THIS call — the one with
	// the session's workspace jail — never the startup pointer.
	reg := RegistryFrom(ctx)
	if reg == nil {
		reg = t.reg
	}
	if reg == nil {
		return "", fmt.Errorf("apply_patch: registry not wired")
	}
	var a struct {
		Path  json.RawMessage `json:"path"`
		Edits []struct {
			Path        *string `json:"path"`
			OldString   *string `json:"old_string"`
			NewString   *string `json:"new_string"`
			ExpectedSHA *string `json:"expected_sha256"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(args, &a); err != nil || len(a.Edits) == 0 {
		return "", fmt.Errorf("edits: non-empty list of {path, old_string, new_string} objects required%s", patchShapeHint)
	}
	if len(a.Edits) > patchMaxHunks {
		return "", fmt.Errorf("%d hunks, max %d — split into multiple apply_patch calls", len(a.Edits), patchMaxHunks)
	}
	if len(a.Path) != 0 {
		return "", fmt.Errorf("top-level path is not supported%s", patchShapeHint)
	}
	hunks := make([]patchHunk, 0, len(a.Edits))
	for i, h := range a.Edits {
		if h.Path == nil || *h.Path == "" {
			return "", fmt.Errorf("hunk %d: path required%s", i+1, patchShapeHint)
		}
		if h.OldString == nil || h.NewString == nil {
			return "", fmt.Errorf("hunk %d (%s): old_string and new_string must both be strings, not omitted or null%s", i+1, *h.Path, patchShapeHint)
		}
		hunks = append(hunks, patchHunk{Path: *h.Path, OldString: *h.OldString, NewString: *h.NewString, ExpectedSHA: h.ExpectedSHA})
	}

	// Phase 1 — validate EVERY hunk before ANY edit lands. Simulate hunks in
	// order against shadow source, so dependent edits work and later conflicts
	// fail before any earlier file is changed. Reads still pass through guards.
	type validated struct {
		hunk   patchHunk
		tool   string // "edit" or "write"
		rawArg json.RawMessage
		proof  *mutationSyntaxProof
	}
	type syntaxFile struct {
		path, target, workspace, before string
	}
	var plan []validated
	shadow := map[string]string{}
	identities := map[string]os.FileInfo{}
	syntaxFiles := map[string]syntaxFile{}
	var syntaxOrder []string
	for i, h := range hunks {
		if len(h.NewString) > maxFileSize {
			return "", fmt.Errorf("hunk %d (%s): replacement too large (max %d bytes) — no edits applied", i+1, h.Path, maxFileSize)
		}
		tool := "edit"
		if h.OldString == "" {
			tool = "write"
		}
		key, j, err := patchTarget(reg, h.Path, tool, mode)
		if err != nil {
			return "", fmt.Errorf("hunk %d (%s): %w — no edits applied", i+1, h.Path, err)
		}
		target := key
		// Symlink/lexical aliases share the canonical key. Existing hard
		// links also share a shadow image because Edit/Write write in place.
		if info, err := os.Stat(key); err == nil {
			for prior, known := range identities {
				if os.SameFile(info, known) {
					key = prior
					break
				}
			}
			identities[key] = info
		}
		supported := len(mutationSyntaxModes(h.Path, target)) > 0
		if _, known := syntaxFiles[key]; supported && !known {
			before := ""
			if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
				if statErr != nil {
					return "", fmt.Errorf("hunk %d (%s): %w — no edits applied", i+1, h.Path, statErr)
				}
				before, err = patchReadSource(ctx, reg, h.Path, mode)
				if err != nil {
					return "", fmt.Errorf("hunk %d (%s): baseline read failed: %w — no edits applied", i+1, h.Path, err)
				}
			}
			syntaxFiles[key] = syntaxFile{h.Path, target, j.root, before}
			syntaxOrder = append(syntaxOrder, key)
		}
		proofFor := func(before, after string) *mutationSyntaxProof {
			if !supported {
				return nil
			}
			return &mutationSyntaxProof{target, sourceSHA256(before), sourceSHA256(after)}
		}
		if h.OldString == "" && h.ExpectedSHA == nil {
			before, staged := shadow[key]
			if !staged && supported {
				before = syntaxFiles[key].before
			}
			shadow[key] = h.NewString
			raw, _ := json.Marshal(map[string]string{"path": h.Path, "content": h.NewString})
			plan = append(plan, validated{h, "write", raw, proofFor(before, h.NewString)})
			continue
		}
		// Never validate against read's numbered, paginated model presentation.
		// The private context flag keeps the call through guards/the session
		// jail while returning complete bounded source without cursor effects.
		content, staged := shadow[key]
		if !staged {
			content, err = patchReadSource(ctx, reg, h.Path, mode)
			if err != nil {
				return "", fmt.Errorf("hunk %d (%s): read failed: %w — no edits applied", i+1, h.Path, err)
			}
		}
		if err := checkSourceVersion(content, h.ExpectedSHA); err != nil {
			return "", fmt.Errorf("hunk %d (%s), against the projected state before this hunk: %w — no edits applied", i+1, h.Path, err)
		}
		if h.OldString == "" {
			shadow[key] = h.NewString
			raw, _ := json.Marshal(map[string]string{"path": h.Path, "content": h.NewString, "expected_sha256": sourceSHA256(content)})
			plan = append(plan, validated{h, "write", raw, proofFor(content, h.NewString)})
			continue
		}
		// Share the exact transformation with Edit, including no-op, unique
		// match, indentation, deletion and size validation.
		next, err := editSource(content, h.Path, h.OldString, h.NewString)
		if err != nil {
			if staged {
				return "", fmt.Errorf("hunk %d (%s), against the projected result of preceding hunks (not yet written): %w — no edits applied", i+1, h.Path, err)
			}
			return "", fmt.Errorf("hunk %d (%s): %w — no edits applied", i+1, h.Path, err)
		}
		shadow[key] = next
		// Bind execution to the prevalidated image even when the caller did not
		// provide a hash. Registry hooks must not silently invalidate the check.
		raw, _ := json.Marshal(map[string]string{"path": h.Path, "old_string": h.OldString, "new_string": h.NewString, "expected_sha256": sourceSHA256(content)})
		plan = append(plan, validated{h, "edit", raw, proofFor(content, next)})
	}
	// Check only the final image of each JS file. No hunk may land before
	// every final image has been checked, including create/overwrite hunks.
	checkCtx, cancel := context.WithTimeout(ctx, mutationSyntaxTimeout)
	defer cancel()
	var syntaxReceipts strings.Builder
	validator := newMutationSyntaxValidator()
	for _, key := range syntaxOrder {
		file := syntaxFiles[key]
		receipt, err := validator.validate(checkCtx, file.workspace, file.path, file.target, file.before, shadow[key])
		if err != nil {
			return receipt, fmt.Errorf("apply_patch final projected syntax: %w", err)
		}
		syntaxReceipts.WriteString(receipt)
	}
	if err := checkCtx.Err(); err != nil {
		return "", fmt.Errorf("apply_patch validation: %w — no edits applied", err)
	}

	// Phase 2 — apply.
	var sb strings.Builder
	if len(syntaxOrder) == 0 {
		sb.WriteString("syntax unverified: no supported JS targets; behavior checks not run\n")
	} else {
		sb.WriteString(syntaxReceipts.String())
	}
	applied := 0
	receiptsOmitted := false
	for _, v := range plan {
		hunkCtx := ctx
		if v.proof != nil {
			hunkCtx = context.WithValue(ctx, mutationSyntaxProofKey{}, *v.proof)
		}
		receipt, err := reg.ExecuteMode(hunkCtx, v.tool, v.rawArg, mode)
		if err != nil {
			// Mid-apply failure: report honestly what landed and what didn't.
			fmt.Fprintf(&sb, "\n!! hunk (%s) FAILED after %d applied: %v", v.hunk.Path, applied, err)
			return sb.String(), fmt.Errorf("apply_patch: hunk %s failed after %d/%d applied: %w", v.hunk.Path, applied, len(plan), err)
		}
		// Preserve exact changed-line/version receipts, not every source excerpt
		// multiplied by twenty. Keep a bounded aggregate tool result.
		metadata := strings.SplitN(receipt, "\n", 2)[0]
		label := fmt.Sprintf("patched %s (%s)\n", v.hunk.Path, v.tool)
		if sb.Len()+len(metadata)+len(label) < 12000 {
			sb.WriteString(metadata)
			sb.WriteByte('\n')
			sb.WriteString(label)
		} else {
			receiptsOmitted = true
		}
		applied++
	}
	if receiptsOmitted {
		sb.WriteString("...[additional per-hunk receipts omitted by the output cap]\n")
	}
	fmt.Fprintf(&sb, "ok: %d/%d hunks applied", applied, len(plan))
	return sb.String(), nil
}

// Resolve identity with the same built-in jail used at write time. This is
// read-only prevalidation, not a substitute for the final registry dispatch:
// guards, remediation and backups are still applied to every actual write.
func patchTarget(reg *Registry, path, tool, mode string) (string, jail, error) {
	entry, ok := reg.Entry(tool)
	if !ok || !reg.allowed(tool, mode) {
		return "", jail{}, fmt.Errorf("%s unavailable in %s mode", tool, mode)
	}
	if mode == ModeDiscussion && !isDesignArtifact(path) {
		return "", jail{}, fmt.Errorf("discussion mode: writes limited to design artifacts")
	}
	var j jail
	switch t := entry.Tool.(type) {
	case *Edit:
		j = t.j
	case *Write:
		j = t.j
	default:
		return "", jail{}, fmt.Errorf("patch validation requires the built-in %s tool", tool)
	}
	full, err := j.resolve(path)
	if err != nil {
		return "", jail{}, err
	}
	target, err := evalExistingPrefix(full)
	return target, j, err
}

func patchReadSource(ctx context.Context, reg *Registry, path, mode string) (string, error) {
	if e, ok := reg.Entry("read"); !ok {
		return "", fmt.Errorf("apply_patch: read tool unavailable")
	} else if _, ok := e.Tool.(*Read); !ok {
		return "", fmt.Errorf("apply_patch: raw validation requires the built-in read tool")
	}
	readRaw, _ := json.Marshal(map[string]string{"path": path})
	return reg.ExecuteMode(context.WithValue(ctx, rawPatchReadKey{}, true), "read", readRaw, mode)
}
