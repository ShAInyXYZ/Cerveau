package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		case "bash", "read", "write", "edit", "grep", "glob", "apply_patch", "web_fetch", "ask_user", "commit_plan", "remember",
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

// A file larger than the read cap must be reachable in full: the truncation
// notice has to say HOW to continue, and offset must return the next slice.
// Without this the model re-read the same head forever and the loop guard
// killed the run ("identical result repeated 3 times").
func TestReadOffsetContinues(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("A", readCapChars) + strings.Repeat("B", 500)
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(body), 0o644)
	r := NewRead(dir)

	head, err := r.Execute(context.Background(), json.RawMessage(`{"path":"big.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(head, "offset") {
		t.Fatalf("truncation notice must tell the model how to continue: %q", head[len(head)-160:])
	}

	tail, err := r.Execute(context.Background(), json.RawMessage(`{"path":"big.txt","offset":8000}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tail, "BBB") {
		t.Fatalf("offset read did not return the continuation: %q", tail[:40])
	}
	if strings.HasPrefix(tail, "AAA") {
		t.Fatal("offset ignored — same head returned again")
	}
}
