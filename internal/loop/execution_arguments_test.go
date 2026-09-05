package loop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
)

type argumentBoundaryTool struct {
	calls int
	args  string
}

func (t *argumentBoundaryTool) Name() string        { return "noargs" }
func (t *argumentBoundaryTool) Description() string { return "argument boundary fixture" }
func (t *argumentBoundaryTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *argumentBoundaryTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	t.calls++
	t.args = string(args)
	return "ok", nil
}

func TestModelArgumentBoundaryRequiresObjectAndJournalsRejections(t *testing.T) {
	for _, tc := range []struct {
		args  string
		valid bool
	}{
		{"", false}, {"{", false}, {"{} {}", false}, {"null", false}, {" \nnull\t", false},
		{"[]", false}, {`"text"`, false}, {"true", false}, {"42", false},
		{"{}", true}, {`{"value":null}`, true},
	} {
		t.Run(tc.args, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.jsonl")
			wr, err := episodic.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer wr.Close()
			tool := &argumentBoundaryTool{}
			reg := tools.NewRegistry(tools.Entry{Tool: tool, RiskTier: tools.RiskSafe})
			l := &Loop{}
			_, callErr, resultID := l.executeCall(context.Background(), wr, reg, reg.Specs(tools.ModeAutopilot), tools.ModeAutopilot,
				llm.ToolCall{ID: "call-1", Type: "function", Function: llm.FunctionCall{Name: tool.Name(), Arguments: tc.args}})
			if (callErr == nil) != tc.valid {
				t.Fatalf("args %q valid=%v: err=%v", tc.args, tc.valid, callErr)
			}
			if !tc.valid && tool.calls != 0 {
				t.Fatalf("rejected model arguments dispatched %d times", tool.calls)
			}
			if tc.valid && (tool.calls != 1 || tool.args != tc.args) {
				t.Fatalf("valid args were changed or not dispatched exactly once: %+v", tool)
			}
			events, err := episodic.Replay(path)
			if err != nil || len(events) != 2 {
				t.Fatalf("journal must contain paired call/result: events=%+v, err=%v", events, err)
			}
			var call struct {
				ID      string `json:"id"`
				RawArgs string `json:"raw_args"`
			}
			var result struct {
				ID string `json:"id"`
				OK bool   `json:"ok"`
			}
			if err := json.Unmarshal(events[0].Payload, &call); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(events[1].Payload, &result); err != nil {
				t.Fatal(err)
			}
			if events[0].Type != episodic.ToolCall || call.RawArgs != tc.args || call.ID != "call-1" ||
				events[1].Type != episodic.ToolResult || result.ID != call.ID || result.OK != tc.valid || resultID != events[1].ID {
				t.Fatalf("journal lost original input or rejection: call=%+v result=%+v", call, result)
			}
		})
	}
}
