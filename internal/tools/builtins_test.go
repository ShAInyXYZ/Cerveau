package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cerveau/internal/guard"
	"cerveau/internal/rfx"
)

// The builtin pack (repo-root rfx/) is dogfood, not decoration: this test
// loads it through the REAL loader, then executes git-status through the
// REAL bash tool and REAL dispatch guard. If the pack drifts out of spec,
// or the executor out of the pack, this fails.
func TestBuiltinPackLoadsAndExecutes(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	packDir := filepath.Join(repoRoot, "rfx")

	known := func(name string) bool {
		switch name {
		case "bash", "read", "write", "edit", "grep", "web_fetch", "ask_user", "commit_plan", "remember",
			"file_map", "find_symbol", "find_references", "outline_file":
			return true
		}
		return false
	}
	l := rfx.NewLoader(packDir, known)
	defs := l.List()
	if len(defs) < 5 {
		t.Fatalf("builtin pack: %d reflexes loaded, want ≥5; errors: %v", len(defs), l.Errors())
	}
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("builtin pack has rejected manifests: %v", errs)
	}

	// Every builtin must carry a fuzz contract (pack discipline).
	for _, d := range defs {
		if d.Contract.MaxMs <= 0 {
			t.Errorf("builtin %q: no contract.max_ms", d.Name)
		}
	}

	// Execute git-status for real against this repo.
	reg := NewRegistry(Entry{Tool: NewBash(repoRoot), RiskTier: RiskDangerous, Modes: []string{ModeAutopilot}})
	grd := guard.New(repoRoot)
	reg.SetGuard(grd.Check)
	reg.SetRemediator(func(tool string, args json.RawMessage) (json.RawMessage, error) {
		return grd.Remediate(tool, args, time.Now())
	})
	if errs := reg.AddReflexes(defs); len(errs) != 0 {
		t.Fatal(errs)
	}
	out, err := reg.ExecuteMode(context.Background(), "git-status", json.RawMessage(`{}`), ModeAutopilot)
	if err != nil {
		t.Fatalf("git-status reflex failed for real: %v\n%s", err, out)
	}
	if !strings.Contains(out, "##") { // git status --branch always prints "## <branch>"
		t.Fatalf("git-status output doesn't look like git status: %q", out)
	}
}
