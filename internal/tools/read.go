package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	return "Read a file from the workspace, with line numbers. Path is relative to the workspace root. " +
		"Use from_line/to_line to read just the part you care about instead of the whole file. " +
		"Large files are returned in slices — call again to continue from where the last read stopped."
}

func (t *Read) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":      map[string]any{"type": "string", "description": "workspace-relative file path"},
			"from_line": map[string]any{"type": "integer", "description": "first line to return (1-based). Use with to_line to read only the region you need"},
			"to_line":   map[string]any{"type": "integer", "description": "last line to return (1-based, inclusive)"},
			"offset":    map[string]any{"type": "integer", "description": "character offset to start from (default 0); repeating a read without it auto-continues"},
		},
		"required": []string{"path"},
	}
}

func (t *Read) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path     string `json:"path"`
		Offset   *int   `json:"offset"`
		FromLine int    `json:"from_line"`
		ToLine   int    `json:"to_line"`
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

	// A LINE RANGE is the targeted path: the model names the region it cares
	// about and pays for nothing else. This is what makes an edit possible
	// without re-reading the file to rebuild its position.
	if a.FromLine > 0 || a.ToLine > 0 {
		lines := strings.Split(full_, "\n")
		from := a.FromLine
		if from < 1 {
			from = 1
		}
		to := a.ToLine
		if to < 1 || to > len(lines) {
			to = len(lines)
		}
		if from > len(lines) {
			return fmt.Sprintf("[from_line %d is past the end — %s has %d lines]", from, a.Path, len(lines)), nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "[%s lines %d-%d of %d]\n", a.Path, from, to, len(lines))
		for i := from; i <= to; i++ {
			fmt.Fprintf(&sb, "%d\t%s\n", i, lines[i-1])
		}
		out := sb.String()
		if len(out) > readCapChars {
			out = out[:readCapChars] + "\n...[range too large — narrow from_line/to_line]"
		}
		return out, nil
	}

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
	return head + numberLines(out, 1+strings.Count(full_[:off], "\n")), nil
}

// numberLines prefixes each line with its 1-based number so the model can
// target an edit by location instead of re-reading to find where it is.
// The trailing notice lines (which start with "...[") are left alone.
func numberLines(s string, start int) string {
	lines := strings.Split(s, "\n")
	var sb strings.Builder
	n := start
	for i, l := range lines {
		if strings.HasPrefix(l, "...[") || strings.HasPrefix(l, "[continuing") {
			sb.WriteString(l)
		} else {
			fmt.Fprintf(&sb, "%d\t%s", n, l)
			n++
		}
		if i < len(lines)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
