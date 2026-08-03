package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// check_page loads a page in a headless browser and reports console errors and
// uncaught exceptions — the feedback the model cannot get any other way. A
// page with a thrown error must produce that error text; a clean page must
// say so.
func TestCheckPageReportsErrors(t *testing.T) {
	if findChrome() == "" {
		t.Skip("no headless chrome on this machine")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.html"), []byte(
		`<!DOCTYPE html><html><body><script>
		console.error("boom-console");
		nonExistentFn();
		</script></body></html>`), 0o644)

	cp := NewCheckPage(dir)
	out, err := cp.Execute(context.Background(), json.RawMessage(`{"path":"bad.html"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "boom-console") {
		t.Errorf("console.error missing from report: %q", out)
	}
	if !strings.Contains(out, "nonExistentFn") {
		t.Errorf("uncaught exception missing from report: %q", out)
	}
}

func TestCheckPageCleanPage(t *testing.T) {
	if findChrome() == "" {
		t.Skip("no headless chrome on this machine")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok.html"), []byte(
		`<!DOCTYPE html><html><body><canvas id="game"></canvas><h1>fine</h1></body></html>`), 0o644)

	cp := NewCheckPage(dir)
	out, err := cp.Execute(context.Background(), json.RawMessage(`{"path":"ok.html","expect":"canvas"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "no console errors") {
		t.Errorf("clean page should report no errors: %q", out)
	}
	if !strings.Contains(out, "found") {
		t.Errorf("expect selector should be confirmed found: %q", out)
	}
}

// A missing expected element must be reported as missing, not silently passed.
func TestCheckPageMissingExpect(t *testing.T) {
	if findChrome() == "" {
		t.Skip("no headless chrome on this machine")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "empty.html"), []byte(
		`<!DOCTYPE html><html><body><p>nothing here</p></body></html>`), 0o644)

	cp := NewCheckPage(dir)
	out, err := cp.Execute(context.Background(), json.RawMessage(`{"path":"empty.html","expect":"canvas"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "NOT found") {
		t.Errorf("missing element should be flagged: %q", out)
	}
}

// Models write CSS-ish selectors (div.app, .app) — the expect check must
// understand class selectors, not report false misses on healthy pages.
func TestCheckPageClassSelector(t *testing.T) {
	dom := `<html><body><div class="app shell"><canvas id="game"></canvas></div></body></html>`
	for _, sel := range []string{"div.app", ".app", "#game", "canvas"} {
		if got := checkExpect("", dom, sel); !strings.Contains(got, "found in the rendered DOM") {
			t.Errorf("selector %q should be found: %q", sel, got)
		}
	}
	if got := checkExpect("", dom, ".missing"); !strings.Contains(got, "NOT found") {
		t.Errorf(".missing should not be found: %q", got)
	}
}
