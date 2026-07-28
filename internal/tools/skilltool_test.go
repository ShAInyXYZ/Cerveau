package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cerveau/internal/guard"
	"cerveau/internal/skills"
)

func TestSkillToolSubstitutionAndRun(t *testing.T) {
	g := guard.New(t.TempDir())
	tls := SkillTools([]skills.SkillTool{{
		Name:        "echo_msg",
		Description: "echo a message",
		Command:     "echo {{msg}}",
	}}, t.TempDir(), g.Check)
	if len(tls) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tls))
	}
	args, _ := json.Marshal(map[string]string{"msg": "hello"})
	out, err := tls[0].Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("output = %q", out)
	}
}

func TestSkillToolUnresolvedTemplate(t *testing.T) {
	g := guard.New(t.TempDir())
	tls := SkillTools([]skills.SkillTool{{Name: "x", Command: "echo {{missing}}"}}, t.TempDir(), g.Check)
	_, err := tls[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("expected unresolved-template error, got %v", err)
	}
}

func TestSkillToolGuardBlocksDangerousCommand(t *testing.T) {
	g := guard.New(t.TempDir())
	// a skill whose command template resolves to a catastrophic delete
	tls := SkillTools([]skills.SkillTool{{
		Name:    "nuke",
		Command: "rm -rf {{target}}",
	}}, t.TempDir(), g.Check)
	args, _ := json.Marshal(map[string]string{"target": "/"})
	_, err := tls[0].Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "guard denied") {
		t.Fatalf("expected guard to block, got %v", err)
	}
}

func TestWithSessionToolsIsolation(t *testing.T) {
	base := NewRegistry(Entry{Tool: NewRead(t.TempDir()), RiskTier: RiskSafe})
	baseCount := len(base.Specs(""))

	g := guard.New(t.TempDir())
	base.SetGuard(g.Check)
	skillTls := SkillTools([]skills.SkillTool{{Name: "run_tests", Command: "echo ok"}}, t.TempDir(), g.Check)
	sess := base.WithSessionTools(skillTls)

	// session registry sees the extra tool
	found := false
	for _, s := range sess.Specs("") {
		if s.Function.Name == "run_tests" {
			found = true
		}
	}
	if !found {
		t.Fatal("session registry missing skill tool")
	}
	// original registry is untouched
	if len(base.Specs("")) != baseCount {
		t.Fatal("base registry was mutated by WithSessionTools")
	}
	// the extra tool actually executes through the session registry
	out, err := sess.ExecuteMode(context.Background(), "run_tests", json.RawMessage(`{}`), "autopilot")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("run_tests output = %q", out)
	}
}
