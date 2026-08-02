package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

const readCapChars = 8000

type Read struct {
	j jail

	// A small local model often ignores the offset hint and simply re-reads
	// the same path, getting the identical head until the loop guard kills
	// the turn. Remember where each path last stopped so an unqualified
	// repeat AUTO-ADVANCES instead of spinning. An explicit offset always
	// wins, and reaching EOF resets the cursor.
	mu   sync.Mutex
	next map[string]int
}

func NewRead(workspaceRoot string) *Read {
	return &Read{j: newJail(workspaceRoot), next: map[string]int{}}
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
		Offset *int   `json:"offset"`
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
	var off int
	auto := false
	switch {
	case a.Offset != nil:
		off = *a.Offset // explicit always wins
		if off < 0 {
			off = 0
		}
	default:
		t.mu.Lock()
		off = t.next[a.Path]
		t.mu.Unlock()
		auto = off > 0
	}
	if off >= len(full_) {
		t.mu.Lock()
		delete(t.next, a.Path) // wrap: a later read starts from the top again
		t.mu.Unlock()
		if auto {
			return fmt.Sprintf("[end of %s reached (%d chars). Reading it again starts from the beginning.]", a.Path, len(full_)), nil
		}
		return fmt.Sprintf("[offset %d is past the end of the file — it is %d chars long]", off, len(full_)), nil
	}
	out := full_[off:]
	head := ""
	if auto {
		head = fmt.Sprintf("[continuing %s from char %d — the previous read stopped here]\n", a.Path, off)
	}
	if len(out) > readCapChars {
		next := off + readCapChars
		t.mu.Lock()
		t.next[a.Path] = next
		t.mu.Unlock()
		out = out[:readCapChars] + fmt.Sprintf(
			"\n...[slice %d-%d of %d chars. Call read again (with or without offset: %d) to continue.]",
			off, next, len(full_), next)
	} else {
		t.mu.Lock()
		delete(t.next, a.Path)
		t.mu.Unlock()
		if off > 0 {
			out += fmt.Sprintf("\n...[end of file, %d chars total]", len(full_))
		}
	}
	return head + out, nil
}
