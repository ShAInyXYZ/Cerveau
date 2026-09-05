package loop

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

type crashAfterEffect struct{ root string }

func (c crashAfterEffect) Name() string { return "crash_fixture" }
func (c crashAfterEffect) Description() string {
	return "test-only process exit after an isolated effect"
}
func (c crashAfterEffect) Schema() map[string]any { return map[string]any{"type": "object"} }
func (c crashAfterEffect) Execute(context.Context, json.RawMessage) (string, error) {
	if err := os.WriteFile(filepath.Join(c.root, "effect.txt"), []byte("effect occurred once"), 0600); err != nil {
		return "", err
	}
	// Deliberately skips all deferred cleanup, exactly between effect and result.
	os.Exit(93)
	return "", nil
}

func TestCrashAfterSideEffectBeforeToolResult(t *testing.T) {
	root := os.Getenv("CERVEAU_CRASH_FIXTURE_DIR")
	if root != "" {
		path := filepath.Join(root, "events.jsonl")
		wr, err := episodic.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		reg := tools.NewRegistry(tools.Entry{Tool: crashAfterEffect{root}, RiskTier: tools.RiskSafe})
		l := New(llm.NewClient("http://unused.invalid"), reg, func(string) (*episodic.Writer, error) { return wr, nil }, func(string) string { return path }, nil)
		l.SetWorkspaceFunc(func(string) string { return root })
		ctx, h, _, err := l.beginRun(context.Background(), "crash", "autopilot", "")
		if err != nil {
			t.Fatal(err)
		}
		l.executeCall(ctx, h.writer, reg, reg.Specs("autopilot"), "autopilot", llm.ToolCall{ID: "crash-call", Type: "function", Function: llm.FunctionCall{Name: "crash_fixture", Arguments: "{}"}})
		t.Fatal("child did not exit at effect boundary")
	}
	root = t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashAfterSideEffectBeforeToolResult$")
	cmd.Env = append(os.Environ(), "CERVEAU_CRASH_FIXTURE_DIR="+root)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 93 {
		t.Fatalf("child exit=%v output=%s", err, output)
	}
	path := filepath.Join(root, "events.jsonl")
	events, err := episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	p := ProjectEvents(events, "")
	if p.Run == nil || p.Run.Status != "interrupted" || p.Running {
		t.Fatalf("crash became success: %+v", p)
	}
	calls, results := 0, 0
	for _, ev := range events {
		if ev.Type == episodic.ToolCall {
			calls++
		}
		if ev.Type == episodic.ToolResult {
			results++
		}
	}
	if calls != 1 || results != 0 {
		t.Fatalf("invented tool outcome: calls=%d results=%d", calls, results)
	}
	if externalRunOwner(path + ".run.lock") {
		t.Fatal("dead process retained lease")
	}
	repaired := window.RepairToolGroups([]window.Item{{Kind: "assistant", Msg: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "crash-call", Type: "function", Function: llm.FunctionCall{Name: "crash_fixture", Arguments: "{}"}}}}}})
	if len(repaired) != 2 || !strings.Contains(repaired[1].Msg.Content, "Outcome not recorded") || !strings.Contains(repaired[1].Msg.Content, "Inspect current state before repeating") {
		t.Fatalf("unknown outcome hidden: %+v", repaired)
	}
	before, _ := os.ReadFile(filepath.Join(root, "effect.txt"))
	for i := 0; i < 3; i++ {
		ProjectEvents(events, "")
	}
	after, _ := os.ReadFile(filepath.Join(root, "effect.txt"))
	if string(before) != "effect occurred once" || string(after) != string(before) {
		t.Fatal("observer replay repeated/mutated side effect")
	}
}
