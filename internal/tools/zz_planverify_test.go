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
		{"existence in a costume", `{"title":"P","steps":[{"title":"a","files":["x.js"],"verify":{"kind":"command","command":"test -f x.js"}}]}`, false},
		{"contains without a symbol", `{"title":"P","steps":[{"title":"a","files":["x.js"],"verify":{"kind":"contains","file":"x.js"}}]}`, false},
		{"a real command", `{"title":"P","steps":[{"title":"a","files":["x.js"],"verify":{"kind":"command","command":"node --check x.js"}}]}`, true},
		{"a real contains", `{"title":"P","steps":[{"title":"a","files":["x.js"],"verify":{"kind":"contains","file":"x.js","symbol":"buildFan"}}]}`, true},
		{"a real eval", `{"title":"P","steps":[{"title":"a","files":["x.js"],"verify":{"kind":"eval","expr":"!!document.querySelector('canvas')","path":"i.html"}}]}`, true},
		// the markdown path stays open for small models
		{"markdown is a draft", `{"markdown":"## Scene setup\nbuild the shell\n\n## Car model\nboxes"}`, false},
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

// A forced tool_choice decodes INSIDE this schema. An empty steps array was
// a legal exit and the model took it; the schema must not permit it.
func TestCommitPlanSchemaRequiresAtLeastOneStep(t *testing.T) {
	sch := commitPlanTool(t).Schema()
	req, _ := sch["required"].([]string)
	var hasSteps bool
	for _, r := range req {
		if r == "steps" {
			hasSteps = true
		}
	}
	if !hasSteps {
		t.Error("steps must be required, or a forced call may commit a title and nothing else")
	}
	steps, _ := sch["properties"].(map[string]any)["steps"].(map[string]any)
	if steps["minItems"] != 1 {
		t.Errorf("steps must declare minItems 1, got %v", steps["minItems"])
	}
	// and the tool itself refuses the empty shape with a message that names it
	if _, err := commitPlanTool(t).Execute(context.Background(), json.RawMessage(`{"title":"P","steps":[]}`)); err == nil || !strings.Contains(err.Error(), "at least one step") {
		t.Errorf("empty steps must be refused by name, got %v", err)
	}
}

// Every step omitted files on the live run. The supervisor's downstream
// re-verify and the disk fallback both need them; under forced decoding the
// schema is the only thing that can insist.
func TestCommitPlanSchemaRequiresFiles(t *testing.T) {
	sch := commitPlanTool(t).Schema()
	item := sch["properties"].(map[string]any)["steps"].(map[string]any)["items"].(map[string]any)
	req, _ := item["required"].([]string)
	var has bool
	for _, r := range req {
		if r == "files" {
			has = true
		}
	}
	if !has {
		t.Error("files must be required on each step")
	}
	files := item["properties"].(map[string]any)["files"].(map[string]any)
	if files["minItems"] != 1 {
		t.Errorf("files must declare minItems 1, got %v", files["minItems"])
	}
}
