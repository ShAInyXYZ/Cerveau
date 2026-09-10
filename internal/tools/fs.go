package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const maxFileSize = 1 << 20

type jail struct {
	root string
}

func newJail(workspaceRoot string) jail {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		abs = workspaceRoot
	}
	return jail{root: abs}
}

func (j jail) resolve(path string) (string, error) {
	clean := filepath.Clean(path)
	full := filepath.Join(j.root, clean)
	if !j.contains(full) {
		return "", fmt.Errorf("path %q escapes the workspace", path)
	}
	// Lexical containment is not enough: a symlink *inside* the workspace can
	// point out of it (e.g. `ln -s /etc link`, then write link/passwd). Resolve
	// symlinks on the deepest existing ancestor and re-check containment against
	// the real target. filepath.Clean above already blocked `..` traversal; this
	// closes the symlink escape.
	real, err := evalExistingPrefix(full)
	if err != nil {
		return "", err
	}
	if !j.contains(real) {
		return "", fmt.Errorf("path %q resolves outside the workspace via a symlink", path)
	}
	return full, nil
}

// contains reports whether p is the jail root or lives beneath it.
func (j jail) contains(p string) bool {
	return p == j.root || strings.HasPrefix(p, j.root+string(filepath.Separator))
}

// evalExistingPrefix resolves symlinks on the longest existing prefix of full.
// The final path may not exist yet (a fresh write), so we walk up to the first
// ancestor that does exist, EvalSymlinks that, then re-append the missing tail.
func evalExistingPrefix(full string) (string, error) {
	tail := ""
	cur := full
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if tail == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, tail), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the filesystem root without finding an existing ancestor.
			return full, nil
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}

type Write struct {
	j jail
}

func NewWrite(workspaceRoot string) *Write { return &Write{j: newJail(workspaceRoot)} }

func (t *Write) Name() string { return "write" }

func (t *Write) Description() string {
	return "Create or overwrite a file in the workspace. Path is relative to the workspace root. JS syntax regressions are refused before writing; submit complete source. Already-broken JS remains repairable with explicit syntax status. Other languages are not syntax checked."
}

func (t *Write) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":            map[string]any{"type": "string"},
			"content":         map[string]any{"type": "string"},
			"expected_sha256": map[string]any{"type": "string", "description": "Optional SHA-256 of the existing source, checked before overwriting. Use edit for small changes."},
		},
		"required": []string{"path", "content"},
	}
}

func (t *Write) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path        string  `json:"path"`
		Content     *string `json:"content"`
		ExpectedSHA *string `json:"expected_sha256"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Path == "" || a.Content == nil {
		return "", fmt.Errorf("path and explicit string content required; omitted or null content is refused — no writes applied")
	}
	if len(*a.Content) > maxFileSize {
		return "", fmt.Errorf("content too large (max %d bytes) — no writes applied", maxFileSize)
	}
	full, err := t.j.resolve(a.Path)
	if err != nil {
		return "", err
	}
	before, readErr := os.ReadFile(full)
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", readErr
	}
	if a.ExpectedSHA != nil {
		if readErr != nil {
			return "", fmt.Errorf("stale source: expected an existing file; no writes applied")
		}
		if err := checkSourceVersion(string(before), a.ExpectedSHA); err != nil {
			return "", err
		}
	}
	syntax, err := validateMutationSyntax(ctx, t.j, a.Path, string(before), *a.Content)
	if err != nil {
		return syntax, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(*a.Content), 0o644); err != nil {
		return "", err
	}
	return mutationReceipt("write", a.Path, string(before), *a.Content) + syntax + fmt.Sprintf("wrote %d bytes to %s; source mutation only, behavior checks not run", len(*a.Content), a.Path), nil
}

type Edit struct {
	j jail
}

func NewEdit(workspaceRoot string) *Edit { return &Edit{j: newJail(workspaceRoot)} }

func (t *Edit) Name() string { return "edit" }

func (t *Edit) Description() string {
	return "Replace a string in a workspace file — the targeted way to change code. " +
		"old_string must appear exactly once; leading indentation does NOT have to match " +
		"(the file's own indentation is preserved). Prefer this over rewriting a file: " +
		"read the region with from_line/to_line, then edit just those lines WITHOUT read's line-number prefixes. Pass read's sha256 as expected_sha256 to refuse stale source. " +
		"JS syntax regressions are refused before writing; use one apply_patch batch when multiple hunks must change together. Already-broken JS remains repairable with explicit syntax status; other languages are not syntax checked. " +
		"To DELETE code, set new_string to an empty string — the matched text (and its line, if now blank) is removed."
}

func (t *Edit) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":            map[string]any{"type": "string"},
			"old_string":      map[string]any{"type": "string"},
			"new_string":      map[string]any{"type": "string"},
			"expected_sha256": map[string]any{"type": "string", "description": "Optional full-file SHA-256 from read metadata. A changed version is refused before replacement; reread the affected region."},
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}

func (t *Edit) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path        string  `json:"path"`
		Old         string  `json:"old_string"`
		New         *string `json:"new_string"`
		ExpectedSHA *string `json:"expected_sha256"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Path == "" || a.Old == "" || a.New == nil {
		return "", fmt.Errorf("path, non-empty old_string and explicit new_string required — no edits applied. Use {\"path\":\"relative/file\",\"old_string\":\"current source\",\"new_string\":\"replacement\"}; deletion requires an explicit empty new_string, never omission or null")
	}
	full, err := t.j.resolve(a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if err := checkSourceVersion(string(data), a.ExpectedSHA); err != nil {
		return "", err
	}
	out, err := editSource(string(data), a.Path, a.Old, *a.New)
	if err != nil {
		return "", err
	}
	syntax, err := validateMutationSyntax(ctx, t.j, a.Path, string(data), out)
	if err != nil {
		return syntax, err
	}
	if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
		return "", err
	}
	return mutationReceipt("edit", a.Path, string(data), out) + syntax + "source mutation only; behavior checks not run", nil
}

func sourceSHA256(source string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
}

// The optional hash binds a replacement to source actually inspected. It is
// a precondition, not an OS-level compare-and-swap against external writers.
func checkSourceVersion(source string, expected *string) error {
	if expected == nil {
		return nil
	}
	decoded, err := hex.DecodeString(*expected)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("expected_sha256 must be a 64-character SHA-256 from read metadata; no edits applied")
	}
	actual := sourceSHA256(source)
	if !strings.EqualFold(actual, *expected) {
		return fmt.Errorf("stale source: expected_sha256 %s does not match current sha256 %s; no edits applied. Re-read the targeted region, then use its current source and sha256", *expected, actual)
	}
	return nil
}

// Mutation receipts describe only the bytes changed, not generated file
// contents or a passing check. The JSON excerpts are bounded even when control
// characters expand on encoding. Line locations come from the actual result.
func mutationReceipt(kind, path, before, after string) string {
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	for prefix > 0 && ((prefix < len(before) && !utf8.RuneStart(before[prefix])) || (prefix < len(after) && !utf8.RuneStart(after[prefix]))) {
		prefix--
	}
	oldEnd, newEnd := len(before), len(after)
	for oldEnd > prefix && newEnd > prefix && before[oldEnd-1] == after[newEnd-1] {
		oldEnd--
		newEnd--
	}
	for oldEnd < len(before) && !utf8.RuneStart(before[oldEnd]) {
		oldEnd++
	}
	for newEnd < len(after) && !utf8.RuneStart(after[newEnd]) {
		newEnd++
	}
	line := 1 + strings.Count(before[:prefix], "\n")
	lastLine := func(s string, end int) int {
		if end <= prefix {
			return line
		}
		return 1 + strings.Count(s[:end-1], "\n")
	}
	meta, _ := json.Marshal(map[string]any{
		"path": path, "before_sha256": sourceSHA256(before), "sha256": sourceSHA256(after),
		"from_line": line, "old_to_line": lastLine(before, oldEnd), "to_line": lastLine(after, newEnd),
		"removed_bytes": oldEnd - prefix, "added_bytes": newEnd - prefix,
	})
	return "[" + kind + " " + string(meta) + "]\n" +
		"before (changed bytes): " + boundedSourceJSON(before[prefix:oldEnd]) + "\n" +
		"after (changed bytes): " + boundedSourceJSON(after[prefix:newEnd]) + "\n"
}

func boundedSourceJSON(source string) string {
	truncated := len(source) > 400
	end := min(len(source), 400)
	for end > 0 && end < len(source) && !utf8.RuneStart(source[end]) {
		end--
	}
	quoted, _ := json.Marshal(source[:end])
	for len(quoted) > 700 {
		end /= 2
		for end > 0 && !utf8.RuneStart(source[end]) {
			end--
		}
		quoted, _ = json.Marshal(source[:end])
		truncated = true
	}
	if truncated {
		return string(quoted) + " [excerpt truncated]"
	}
	return string(quoted)
}

// editSource is shared by direct edits and sequential patch prevalidation.
// It is pure: a failed match/validation cannot change a file.
func editSource(content, path, old, new string) (string, error) {
	if len(content) > maxFileSize {
		return "", fmt.Errorf("file too large (%d bytes)", len(content))
	}
	if old == new {
		return "", fmt.Errorf("old_string and new_string are identical — this edit would change nothing; the strings are already equal, so no edit is needed")
	}
	count := matchCountFlexible(content, old)
	if count > 1 {
		return "", fmt.Errorf("old_string matches %d times in %s (must match exactly once) — include a surrounding line to make it unique", count, path)
	}
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s%s", path, nearestHint(content, old))
	}
	var out string
	if strings.Count(content, old) == 1 {
		out = spliceReplace(content, old, new)
	} else {
		out, _ = replaceIgnoringIndent(content, old, new)
	}
	if len(out) > maxFileSize {
		return "", fmt.Errorf("edited file too large (%d bytes; max %d)", len(out), maxFileSize)
	}
	return out, nil
}

// matchCountFlexible reports how many places old_string occurs, counting an
// exact match OR an indentation-only match (each line trimmed). apply_patch
// uses this so its prevalidation agrees with what edit will
// actually do — otherwise a hunk validates one way and applies another.
func matchCountFlexible(content, old string) int {
	if n := strings.Count(content, old); n > 0 {
		return n
	}
	return len(indentMatchStarts(content, old))
}

// spliceReplace replaces the single occurrence of old with new. When new is
// empty (a deletion) and old sat alone on its line, the whole line — its
// leading indentation and trailing newline included — is removed, so a
// deletion never leaves a dangling blank line behind. Any other replacement
// is a plain substring swap.
func spliceReplace(content, old, new string) string {
	if new != "" {
		return strings.Replace(content, old, new, 1)
	}
	idx := strings.Index(content, old)
	if idx < 0 {
		return content
	}
	lineStart := strings.LastIndexByte(content[:idx], '\n') + 1
	lineEnd := idx + len(old)
	if nl := strings.IndexByte(content[lineEnd:], '\n'); nl >= 0 {
		lineEnd += nl + 1
	}
	// If old was the only non-whitespace on its line, remove the whole line.
	if strings.TrimSpace(content[lineStart:idx]) == "" &&
		strings.TrimSpace(content[idx+len(old):lineEnd]) == "" {
		return content[:lineStart] + content[lineEnd:]
	}
	return content[:idx] + content[idx+len(old):]
}

// indentMatchStarts counts all whitespace-tolerant candidates; never select
// the first of several candidates just because exact matching failed.
func indentMatchStarts(content, old string) []int {
	oldLines := strings.Split(strings.TrimRight(old, "\n"), "\n")
	for i := range oldLines {
		oldLines[i] = strings.TrimSpace(oldLines[i])
	}
	fileLines := strings.Split(content, "\n")
	var matches []int
	for start := 0; start+len(oldLines) <= len(fileLines); start++ {
		hit := true
		for j, ol := range oldLines {
			if strings.TrimSpace(fileLines[start+j]) != ol {
				hit = false
				break
			}
		}
		if !hit {
			continue
		}
		matches = append(matches, start)
	}
	return matches
}

// replaceIgnoringIndent uses the existing indentation-tolerant transform,
// but only when its match is unique. Exact source matches take precedence.
func replaceIgnoringIndent(content, old, new string) (string, bool) {
	starts := indentMatchStarts(content, old)
	if len(starts) != 1 {
		return "", false
	}
	start := starts[0]
	fileLines := strings.Split(content, "\n")
	oldLineCount := len(strings.Split(strings.TrimRight(old, "\n"), "\n"))
	merged := append([]string{}, fileLines[:start]...)
	if new != "" { // deletion drops the lines entirely
		indent := fileLines[start][:len(fileLines[start])-len(strings.TrimLeft(fileLines[start], " \t"))]
		newLines := strings.Split(strings.TrimRight(new, "\n"), "\n")
		for j := range newLines {
			newLines[j] = indent + strings.TrimLeft(newLines[j], " \t")
		}
		merged = append(merged, newLines...)
	}
	merged = append(merged, fileLines[start+oldLineCount:]...)
	return strings.Join(merged, "\n"), true
}

// nearestHint offers a bounded CURRENT source region, not a fuzzy patch.
// The caller has already refused the edit. Source is JSON-quoted to keep
// whitespace unambiguous and avoid confusing it with read's numbered output.
func nearestHint(content, old string) string {
	probe := strings.TrimSpace(strings.Split(strings.TrimSpace(old), "\n")[0])
	if len(probe) < 6 {
		return ""
	}
	// progressively shorter prefixes: find the closest thing that does exist
	for n := len(probe); n >= 6; n -= max(1, len(probe)/8) {
		frag := probe[:n]
		if idx := strings.Index(content, frag); idx >= 0 {
			line := 1 + strings.Count(content[:idx], "\n")
			lineStart := strings.LastIndex(content[:idx], "\n") + 1
			lines := strings.SplitN(content[lineStart:], "\n", 7)
			if len(lines) > 6 {
				lines = lines[:6]
			}
			// Limit the encoded representation as well as the raw bytes, since
			// control characters and quotes expand in JSON diagnostics.
			actual := strings.Join(lines, "\n")
			if len(actual) > 700 {
				actual = actual[:700]
			}
			quoted, _ := json.Marshal(actual)
			for len(quoted) > 1400 {
				actual = actual[:len(actual)/2]
				quoted, _ = json.Marshal(actual)
			}
			return fmt.Sprintf(" — candidate begins at line %d; current unnumbered source excerpt (JSON string, may be partial): %s. This is not an exact match. Re-read that region if needed and submit a smaller unique hunk using the current text; omit read's line-number prefixes. No fuzzy replacement was applied.", line, quoted)
		}
	}
	return " — nothing similar found; re-read the region with from_line/to_line to see the current text"
}
