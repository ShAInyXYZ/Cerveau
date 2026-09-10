package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
)

func TestRetryStartsReadCursorAtBeginning(t *testing.T) {
	m := newScriptedModel(
		toolCall("read", `{"path":"long.js"}`),
		toolCall("read", `{"path":"long.js"}`),
		textReply("work remains incomplete"),
		toolCall("read", `{"path":"long.js"}`),
		textReply("work remains incomplete"),
	)
	defer m.srv.Close()
	l, path := gateFixture(t, m)
	if err := os.WriteFile(filepath.Join(l.workspace("s1"), "long.js"), []byte("// FILE START\n"+strings.Repeat("// filler line\n", 2200)), 0600); err != nil {
		t.Fatal(err)
	}
	wr, err := episodic.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = wr.Append(episodic.Plan, map[string]any{"title": "read recovery", "steps": []map[string]any{
		{"title": "implement missing behavior", "files": []string{"long.js"}, "verify": map[string]any{"kind": "contains", "file": "long.js", "symbol": "export function implemented"}},
	}})
	wr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.RunAutopilot(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	events, err := episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	var reads []string
	for _, event := range events {
		if event.Type != episodic.ToolResult {
			continue
		}
		var p struct {
			Name, Output string
			OK           bool
		}
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Name == "read" && p.OK {
			reads = append(reads, p.Output)
		}
	}
	if len(reads) != 3 {
		t.Fatalf("want 2 reads then fresh-attempt read, got %d", len(reads))
	}
	var offsets []int
	for _, out := range reads {
		header, _, _ := strings.Cut(out, "\n")
		var receipt struct {
			StartOffset int `json:"start_offset"`
			EndOffset   int `json:"end_offset"`
		}
		if !strings.HasPrefix(header, "[read ") || json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(header, "[read "), "]")), &receipt) != nil {
			t.Fatalf("missing read receipt: %.200s", out)
		}
		offsets = append(offsets, receipt.StartOffset)
	}
	if !strings.Contains(reads[0], "FILE START") || offsets[0] != 0 || offsets[1] <= 0 || strings.Contains(reads[1], "FILE START") {
		t.Fatal("reads within one attempt must still auto-advance")
	}
	if !strings.Contains(reads[2], "FILE START") || offsets[2] != 0 {
		t.Fatalf("fresh retry inherited old cursor: %.180s", reads[2])
	}
}
