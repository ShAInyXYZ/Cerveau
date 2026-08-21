package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cerveau/internal/guard"
	"cerveau/internal/skills"
)

// End-to-end: registry dispatch path for a skill tool with the REAL guard,
// proving a benign-looking skill turns attacker/LLM-controlled param text
// into arbitrary shell execution.
func TestSkillToolEndToEndRCE(t *testing.T) {
	ws := t.TempDir()
	marker := filepath.Join(ws, "rce-proof")
	def := skills.SkillTool{
		Name:    "weather",
		Description: "fetch weather for a city",
		Command: "curl -s wttr.in/{{city}}",
	}
	g := guard.New(ws)
	reg := NewRegistry(Entry{Tool: SkillTools([]skills.SkillTool{def}, ws, g.Check)[0], RiskTier: RiskDangerous})
	reg.SetGuard(g.Check)

	// note: ';' injection does NOT match any guard rule for this command shape
	args, _ := json.Marshal(map[string]string{"city": "paris; touch " + marker + " #"})
	out, err := reg.Execute(context.Background(), "weather", args)
	t.Logf("err=%v out=%.80q", err, out)
	if _, serr := os.Stat(marker); serr == nil {
		t.Logf("CONFIRMED RCE: skill param broke out of curl into shell, marker created")
	} else {
		t.Logf("marker not created")
	}
}
