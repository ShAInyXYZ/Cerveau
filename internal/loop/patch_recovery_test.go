package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/tools"
)

// Reproduce the benchmark's read -> multiline patch -> verify path through
// the real dispatcher, using only a scripted model and a disposable file.
func TestStepMultilinePatchAfterTargetedRead(t *testing.T) {
	patch, err := json.Marshal(map[string]any{"edits": []map[string]string{
		{"path": "world.js", "old_string": "function lightAt() {\n  return oldValue;\n}", "new_string": "function lightAt() {\n  return newValue;\n}"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m := newScriptedModel(
		toolCall("read", `{"path":"world.js","from_line":1201,"to_line":1203}`),
		toolCall("apply_patch", string(patch)),
		textReply("updated the targeted function"),
	)
	defer m.srv.Close()
	l, path := gateFixture(t, m)
	ws := l.workspace("s1")
	source := strings.Repeat("// filler line\n", 1200) + "function lightAt() {\n  return oldValue;\n}\n"
	if err := os.WriteFile(filepath.Join(ws, "world.js"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(append(l.registry().Entries(),
		tools.Entry{Tool: tools.NewEdit(ws), RiskTier: tools.RiskSensitive, Modes: []string{tools.ModeAutopilot}},
		tools.Entry{Tool: tools.NewApplyPatch(), RiskTier: tools.RiskSensitive, Modes: []string{tools.ModeAutopilot}},
	)...)
	l.SetRegistry(reg)
	wr, err := episodic.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = wr.Append(episodic.Plan, map[string]any{"title": "patch recovery fixture", "steps": []map[string]any{
		{"title": "update function", "files": []string{"world.js"}, "verify": map[string]any{"kind": "contains", "file": "world.js", "symbol": "return newValue;"}},
	}})
	wr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.RunAutopilot(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	state, err := l.PlanStateOf("s1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Done || state.Steps[0].Attempts != 1 {
		t.Fatalf("valid multiline patch must pass without consuming a retry: %+v", state)
	}
	got, err := os.ReadFile(filepath.Join(ws, "world.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.Replace(source, "return oldValue;", "return newValue;", 1) {
		t.Fatal("patch changed unrelated bytes")
	}
	events, err := episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	var successfulPatch bool
	for _, event := range events {
		if event.Type != episodic.ToolResult {
			continue
		}
		var result struct {
			Name string
			OK   bool
		}
		if err := json.Unmarshal(event.Payload, &result); err != nil {
			t.Fatal(err)
		}
		if result.Name == "apply_patch" {
			successfulPatch = result.OK
		}
	}
	if !successfulPatch {
		t.Fatal("successful patch must be recorded in canonical tool results")
	}
}
