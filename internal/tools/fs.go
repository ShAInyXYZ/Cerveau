package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	return "Create or overwrite a file in the workspace. Path is relative to the workspace root."
}

func (t *Write) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		},
		"required": []string{"path", "content"},
	}
}

func (t *Write) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Path == "" {
		return "", fmt.Errorf("path and content required")
	}
	full, err := t.j.resolve(a.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
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
		"read the region with from_line/to_line, then edit just those lines."
}

func (t *Edit) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":       map[string]any{"type": "string"},
			"old_string": map[string]any{"type": "string"},
			"new_string": map[string]any{"type": "string"},
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}

func (t *Edit) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
		Old  string `json:"old_string"`
		New  string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Path == "" || a.Old == "" {
		return "", fmt.Errorf("path, old_string and new_string required")
	}
	if a.Old == a.New {
		return "", fmt.Errorf("old_string and new_string are identical")
	}
	full, err := t.j.resolve(a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if len(data) > maxFileSize {
		return "", fmt.Errorf("file too large (%d bytes)", len(data))
	}
	content := string(data)
	count := strings.Count(content, a.Old)
	if count > 1 {
		return "", fmt.Errorf("old_string matches %d times in %s — include a surrounding line to make it unique", count, a.Path)
	}
	var out string
	switch {
	case count == 1:
		out = strings.Replace(content, a.Old, a.New, 1)
	default:
		// Exact match failed. A model reproducing a line from memory usually
		// gets the CONTENT right and the INDENTATION wrong; demanding byte
		// equality is the main reason it re-reads a file before every edit.
		// Retry ignoring leading whitespace, preserving the file's own.
		rewritten, ok := replaceIgnoringIndent(content, a.Old, a.New)
		if !ok {
			return "", fmt.Errorf("old_string not found in %s%s", a.Path, nearestHint(content, a.Old))
		}
		out = rewritten
	}
	if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", a.Path), nil
}

// replaceIgnoringIndent matches old_string against the file with each line's
// leading whitespace stripped, then splices new_string in using the file's
// ORIGINAL indentation for the first line. Content is what the model
// remembers reliably; exact spaces are not.
func replaceIgnoringIndent(content, old, new string) (string, bool) {
	oldLines := strings.Split(strings.TrimRight(old, "\n"), "\n")
	for i := range oldLines {
		oldLines[i] = strings.TrimSpace(oldLines[i])
	}
	fileLines := strings.Split(content, "\n")
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
		indent := fileLines[start][:len(fileLines[start])-len(strings.TrimLeft(fileLines[start], " \t"))]
		newLines := strings.Split(strings.TrimRight(new, "\n"), "\n")
		for j := range newLines {
			newLines[j] = indent + strings.TrimLeft(newLines[j], " \t")
		}
		merged := append([]string{}, fileLines[:start]...)
		merged = append(merged, newLines...)
		merged = append(merged, fileLines[start+len(oldLines):]...)
		return strings.Join(merged, "\n"), true
	}
	return "", false
}

// nearestHint points at the line that most resembles what the model was
// looking for, so a failed edit does not force a whole-file re-read.
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
			actual := strings.SplitN(content[idx-min(idx, 0):], "\n", 2)[0]
			lineStart := strings.LastIndex(content[:idx], "\n") + 1
			actual = strings.SplitN(content[lineStart:], "\n", 2)[0]
			return fmt.Sprintf(" — nearest match is line %d: %q. Copy that text exactly (indentation is ignored).", line, actual)
		}
	}
	return " — nothing similar found; re-read the region with from_line/to_line to see the current text"
}
