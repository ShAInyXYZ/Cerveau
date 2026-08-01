package tools

import (
	"context"
	"path/filepath"
	"testing"

	"cerveau/internal/rfx"
)

// The builtin pack (repo-root rfx/) is dogfood, not decoration: every file
// in it must load through the REAL loader, carry a fuzz contract, and pass
// a REAL fuzz run. If the pack drifts out of spec, this fails — regardless
// of which reflexes the pack currently contains.
func TestBuiltinPackLoadsAndFuzzes(t *testing.T) {
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
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("builtin pack has rejected manifests: %v", errs)
	}
	if len(defs) == 0 {
		t.Skip("builtin pack is empty (ground-up redesign) — loader gate stands")
	}

	for _, d := range defs {
		if d.Contract.MaxMs <= 0 {
			t.Errorf("builtin %q: no contract.max_ms (pack discipline)", d.Name)
		}
		rep := FuzzReflex(context.Background(), d, rfx.GenerateArgs(d.Params, 100))
		if fails := rep.Failures(); len(fails) != 0 {
			t.Errorf("builtin %q fails its own fuzz contract: %v", d.Name, fails)
		}
	}
}
