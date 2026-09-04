package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cerveau/internal/episodic"
)

func commitPlanTool(t *testing.T) *CommitPlan {
	t.Helper()
	dir := t.TempDir()
	sctx := &SessionContext{SessionID: "s1"}
	open := func(string) (*episodic.Writer, error) { return episodic.Open(dir + "/events.jsonl") }
	return NewCommitPlan(open, sctx)
}

func TestCommitPlanRejectsCostumeVerify(t *testing.T) {
	tool := commitPlanTool(t)
	cases := []struct {
		name string
		args string
		ok   bool
	}{
		{"no verify at all", `{"title":"P","steps":[{"title":"a","files":["x.js"]}]}`, false},
		{"existence in a costume", `{"title":"P","steps":[{"title":"a","verify":{"kind":"command","command":"test -f x.js"}}]}`, false},
		{"contains without a symbol", `{"title":"P","steps":[{"title":"a","verify":{"kind":"contains","file":"x.js"}}]}`, false},
		{"a real command", `{"title":"P","steps":[{"title":"a","verify":{"kind":"command","command":"node --check x.js"}}]}`, true},
		{"a real contains", `{"title":"P","steps":[{"title":"a","verify":{"kind":"contains","file":"x.js","symbol":"buildFan"}}]}`, true},
		{"a real eval", `{"title":"P","steps":[{"title":"a","verify":{"kind":"eval","expr":"!!document.querySelector('canvas')","path":"i.html"}}]}`, true},
		// the markdown path stays open for small models
		{"markdown is exempt", `{"markdown":"## Scene setup\nbuild the shell\n\n## Car model\nboxes"}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := tool.Execute(context.Background(), json.RawMessage(c.args))
			if c.ok && err != nil {
				t.Fatalf("want accepted, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("want rejected, got %q", out)
			}
			if !c.ok && !strings.Contains(err.Error(), "step 1") {
				t.Errorf("error should name the step: %v", err)
			}
		})
	}
}

// The verify must survive the round trip into the plan event, or the supervisor
// has nothing to run.
func TestCommitPlanPersistsVerify(t *testing.T) {
	tool := commitPlanTool(t)
	_, err := tool.Execute(context.Background(), json.RawMessage(
		`{"title":"P","steps":[{"title":"a","files":["x.js"],"verify":{"kind":"contains","file":"x.js","symbol":"buildFan"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
}
