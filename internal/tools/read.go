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
	return "Read a file from the workspace. Path is relative to the workspace root."
}

func (t *Read) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "workspace-relative file path"},
		},
		"required": []string{"path"},
	}
}

func (t *Read) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
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
	out := string(data)
	if len(out) > readCapChars {
		out = out[:readCapChars] + fmt.Sprintf("\n...[truncated, %d chars total]", len(data))
	}
	return out, nil
}
