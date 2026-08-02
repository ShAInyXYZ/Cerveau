package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

const readCapChars = 8000

type Read struct {
	j jail
}

func NewRead(workspaceRoot string) *Read {
	return &Read{j: newJail(workspaceRoot)}
}

func (t *Read) Name() string { return "read" }

func (t *Read) Description() string {
	return "Read a file from the workspace. Path is relative to the workspace root. " +
		"Large files are returned in slices — pass offset to continue from where the last read stopped."
}

func (t *Read) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "workspace-relative file path"},
			"offset": map[string]any{"type": "integer", "description": "character offset to start from (default 0); use the value the truncation notice gives you to read the next slice"},
		},
		"required": []string{"path"},
	}
}

func (t *Read) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Path == "" {
		return "", fmt.Errorf("path required")
	}
	full, err := t.j.resolve(a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	full_ := string(data)
	off := a.Offset
	if off < 0 {
		off = 0
	}
	if off >= len(full_) {
		return fmt.Sprintf("[offset %d is past the end of the file — it is %d chars long]", off, len(full_)), nil
	}
	out := full_[off:]
	if len(out) > readCapChars {
		next := off + readCapChars
		out = out[:readCapChars] + fmt.Sprintf(
			"\n...[slice %d-%d of %d chars. To continue, call read again with offset: %d]",
			off, next, len(full_), next)
	} else if off > 0 {
		out += fmt.Sprintf("\n...[end of file, %d chars total]", len(full_))
	}
	return out, nil
}
