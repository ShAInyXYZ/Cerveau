package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
	if !strings.Contains(head, "slice") {
		t.Fatalf("truncation notice must tell the model how to continue: %q", head[len(head)-160:])
	}

	tail, err := r.Execute(context.Background(), json.RawMessage(`{"path":"big.txt","offset":8000}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tail, "BBB") {
		t.Fatalf("offset read did not return the continuation: %q", tail[:60])
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
	if !strings.Contains(first, "1\tAAA") {
		t.Fatalf("first read should start at line 1 of the head: %q", first[:20])
	}
	second, _ := r.Execute(context.Background(), call)
	if strings.Contains(second, "1\tAAA") {
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
	if !strings.Contains(explicit, "1\tAAA") {
		t.Fatalf("explicit offset:0 must return the head: %q", explicit[:20])
	}
}

// read must show LINE NUMBERS: without them a model cannot target an edit by
// location and re-reads whole files to reconstruct where it is.
func TestReadShowsLineNumbers(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.js"), []byte("alpha\nbeta\ngamma\n"), 0o644)
	out, err := NewRead(dir).Execute(context.Background(), json.RawMessage(`{"path":"a.js"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1\talpha", "2\tbeta", "3\tgamma"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing numbered line %q in:\n%s", want, out)
		}
	}
}

// read must be able to return a WINDOW around a location, so checking one
// function does not cost a whole-file read.
func TestReadLineRange(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&sb, "line%d\n", i)
	}
	os.WriteFile(filepath.Join(dir, "b.js"), []byte(sb.String()), 0o644)

	out, err := NewRead(dir).Execute(context.Background(), json.RawMessage(`{"path":"b.js","from_line":10,"to_line":13}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "10\tline10") || !strings.Contains(out, "13\tline13") {
		t.Fatalf("range not honoured:\n%s", out)
	}
	if strings.Contains(out, "line9\n") || strings.Contains(out, "\tline14") {
		t.Fatalf("range leaked outside 10-13:\n%s", out)
	}
}

// edit must tolerate INDENTATION drift: a model that reproduces a line with
// slightly different leading whitespace should still land the edit. Exact
// byte matching is the main reason it re-reads files before every change.
func TestEditForgivesIndentation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "m.js"), []byte("function f() {\n    cpu.setDifficulty(x);\n}\n"), 0o644)
	e := NewEdit(dir)

	// old_string has NO leading spaces; the file has four.
	out, err := e.Execute(context.Background(), json.RawMessage(
		`{"path":"m.js","old_string":"cpu.setDifficulty(x);","new_string":"ai.setDifficulty(x);"}`))
	if err != nil {
		t.Fatalf("indentation-only mismatch should still edit: %v (%s)", err, out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "m.js"))
	if !strings.Contains(string(got), "    ai.setDifficulty(x);") {
		t.Fatalf("indentation not preserved: %q", string(got))
	}
}

// A failed edit must say WHERE the near-miss is, so the model can fix its
// old_string instead of re-reading the whole file to hunt for it.
func TestEditFailureShowsNearestMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "m.js"), []byte("a\nb\n  cpu.setDifficulty(menuState.difficulty);\nc\n"), 0o644)
	e := NewEdit(dir)
	_, err := e.Execute(context.Background(), json.RawMessage(
		`{"path":"m.js","old_string":"cpu.setDifficulty(WRONG)","new_string":"x"}`))
	if err == nil {
		t.Fatal("expected a failure for a non-matching old_string")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("failure should point at the nearest line: %v", err)
	}
}

// grep should accept a glob filter so a model can scope to *.js instead of
// walking (and dumping matches from) every file type.
func TestGrepGlobFilter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.js"), []byte("TARGET here\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("TARGET there\n"), 0o644)
	g := NewGrep(dir)

	all, _ := g.Execute(context.Background(), json.RawMessage(`{"pattern":"TARGET"}`))
	if !strings.Contains(all, "a.js") || !strings.Contains(all, "b.md") {
		t.Fatalf("unfiltered grep should hit both: %q", all)
	}
	js, _ := g.Execute(context.Background(), json.RawMessage(`{"pattern":"TARGET","glob":"*.js"}`))
	if !strings.Contains(js, "a.js") || strings.Contains(js, "b.md") {
		t.Fatalf("glob *.js should hit only a.js: %q", js)
	}
}

// A zero-match grep must help the model tell "wrong pattern" from "searched
// the wrong place" instead of dead-ending on "no matches".
func TestGrepEmptyIsActionable(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.js"), []byte("hello\n"), 0o644)
	out, _ := NewGrep(dir).Execute(context.Background(), json.RawMessage(`{"pattern":"nowhere"}`))
	if !strings.Contains(out, "no matches") || !strings.Contains(out, "searched") {
		t.Fatalf("empty grep should say what it searched: %q", out)
	}
}
