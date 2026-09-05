package tools

import (
	"cerveau/internal/episodic"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestLifecycleL06MarkdownCannotBypassStructuredValidation(t *testing.T) {
	wr, err := episodic.Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer wr.Close()
	tool := NewCommitPlan(func(string) (*episodic.Writer, error) { return wr, nil }, &SessionContext{SessionID: "s"})
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"title":"bad","markdown":"anything","steps":[{"title":"unchecked","files":["x"]}]}`))
	if err == nil {
		t.Fatal("structured step with no verify accepted because an unrelated markdown field is nonempty")
	}
}
