package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The model needs to ask the page a QUESTION, not just "did it render".
//
// Every fan build hits this: is blade omega really 14 rad/s? did yaw move after
// toggling oscillate? check_page could only report console errors and element
// existence, so the model went hunting for a browser driver instead — 26 of 46
// tool calls in the v10 run were bash probes for playwright/puppeteer, neither
// of which Cerveau has. It burned the whole run improvising a tool.
func TestCheckPageEvalReturnsValues(t *testing.T) {
	dir := t.TempDir()
	page := `<!doctype html><html><body><div id="x">hi</div>
<script>window.__state = { omega: 14, on: true };</script></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "p.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := NewCheckPage(dir)
	args, _ := json.Marshal(map[string]string{
		"path": "p.html",
		"eval": "JSON.stringify(window.__state)",
	})
	out, err := cp.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "14") {
		t.Errorf("eval result never came back — the model cannot read page state:\n%s", out)
	}
}

// A page without eval must behave exactly as before.
func TestCheckPageWithoutEvalUnchanged(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "p.html"), []byte(`<!doctype html><html><body><canvas></canvas></body></html>`), 0o644)
	cp := NewCheckPage(dir)
	args, _ := json.Marshal(map[string]string{"path": "p.html", "expect": "canvas"})
	out, err := cp.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "canvas") {
		t.Errorf("element check regressed:\n%s", out)
	}
}
