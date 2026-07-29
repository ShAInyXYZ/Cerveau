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
	return "Replace an exact string in a workspace file. The old_string must match exactly once."
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
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s", a.Path)
	}
	if count > 1 {
		return "", fmt.Errorf("old_string matches %d times — provide more context", count)
	}
	out := strings.Replace(content, a.Old, a.New, 1)
	if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", a.Path), nil
}
