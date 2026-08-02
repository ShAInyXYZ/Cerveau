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

// A model that ignores the offset hint and re-reads the same path must not
// get the identical head forever: the tool AUTO-ADVANCES to the next slice.
// Advice alone did not work on a small local model — it kept repeating the
// call until the loop guard killed the turn.
func TestReadAutoAdvancesOnRepeat(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("A", readCapChars) + strings.Repeat("B", readCapChars) + "TAIL"
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(body), 0o644)
	r := NewRead(dir)
	call := json.RawMessage(`{"path":"big.txt"}`)

	first, _ := r.Execute(context.Background(), call)
	if !strings.HasPrefix(first, "AAA") {
		t.Fatalf("first read should start at 0: %q", first[:10])
	}
	second, _ := r.Execute(context.Background(), call)
	if strings.HasPrefix(second, "AAA") {
		t.Fatal("identical repeat returned the same head — no auto-advance")
	}
	if !strings.Contains(second, "continuing") || !strings.Contains(second, "BBB") {
		t.Fatalf("second read should announce and return the next slice: %q", second[:60])
	}
	third, _ := r.Execute(context.Background(), call)
	if !strings.Contains(third, "TAIL") {
		t.Fatalf("third read should reach the tail: %q", third[:40])
	}
	// An EXPLICIT offset always wins over auto-advance.
	explicit, _ := r.Execute(context.Background(), json.RawMessage(`{"path":"big.txt","offset":0}`))
	if !strings.HasPrefix(explicit, "AAA") {
		t.Fatalf("explicit offset:0 must return the head: %q", explicit[:10])
	}
}
