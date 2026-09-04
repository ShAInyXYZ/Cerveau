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

// A thrown Error must say WHERE in the eval it threw, and a thrown bare value
// must be called out — three identical evals in a row (2026-09-04) were the
// model resending a script whose only feedback was "EVAL ERROR: no mv".
func TestCheckPageEvalReportsWhereItThrew(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.html"), []byte(`<!doctype html><html><body><div id="x"></div></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := NewCheckPage(dir)
	run := func(expr string) string {
		args, _ := json.Marshal(map[string]string{"path": "p.html", "eval": expr})
		out, err := cp.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return out
	}
	out := run("(function(){ var a = 1;\n var b = 2;\n throw new Error('step 3: e1-f1'); })()")
	if !strings.Contains(out, "step 3: e1-f1") || !strings.Contains(out, "eval line") {
		t.Errorf("thrown Error should report its message and eval line:\n%s", out)
	}
	out = run("(function(){ throw 'no mv'; })()")
	if !strings.Contains(out, "no mv") || !strings.Contains(out, "bare value") {
		t.Errorf("a thrown string should be flagged as carrying no location:\n%s", out)
	}
}

// An eval that returns a Promise reports what it resolves to, not "[object
// Promise]": the async test the model naturally writes now works in one call.
func TestCheckPageEvalAwaitsPromises(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.html"), []byte(`<!doctype html><html><body><div id="x"></div></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := NewCheckPage(dir)
	args, _ := json.Marshal(map[string]string{"path": "p.html",
		"eval": "new Promise(function(r){ setTimeout(function(){ r({after: 'frames', ok: true}); }, 300); })"})
	out, err := cp.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"after":"frames"`) {
		t.Errorf("promise result never came back:\n%s", out)
	}
}

// Top-level await is what the model writes for a test that waits for
// frames; it must work and its `return` must be the reported value.
func TestCheckPageEvalAllowsTopLevelAwait(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.html"), []byte(`<!doctype html><html><body><div id="x"></div></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := NewCheckPage(dir)
	args, _ := json.Marshal(map[string]string{"path": "p.html",
		"eval": "const results = [];\nawait new Promise(r => setTimeout(r, 200));\nresults.push('tilt ' + 1.5);\nreturn results;"})
	out, err := cp.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "tilt 1.5") {
		t.Errorf("top-level await result never came back:\n%s", out)
	}
}

// A bare top-level `return` (no await) must also be wrapped, not rejected.
func TestCheckPageEvalAllowsTopLevelReturn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.html"), []byte(`<!doctype html><html><body><div id="x"></div></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	cp := NewCheckPage(dir)
	args, _ := json.Marshal(map[string]string{"path": "p.html", "eval": "const results = ['a'];\nresults.push('b');\nreturn results;"})
	out, err := cp.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `["a","b"]`) {
		t.Errorf("top-level return should work:\n%s", out)
	}
}

// An ES-module page can ONLY be verified over http. Over file:// the browser
// treats every import as cross-origin and refuses it, so the page renders
// nothing and every probe reports "no canvas" however correct the code is.
//
// eval used to be refused on url:, which left the model with two half-tools —
// url: could see the page but not probe it, path: could probe but never loaded
// the modules. The car run spent eight iterations discovering that before the
// guard killed the turn (2026-09-04).
func TestEvalOverServedURLReachesModuleState(t *testing.T) {
	if findChrome() == "" {
		t.Skip("no headless browser")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mod.js"), []byte(
		"export function build(){ window.__built = {ok:true, blades:5}; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(
		`<!DOCTYPE html><html><body><script type="module">
		   import { build } from './mod.js'; build();
		 </script></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := NewServe(dir)
	if _, err := srv.Execute(context.Background(), json.RawMessage(`{"action":"start","port":8932}`)); err != nil {
		t.Fatalf("serve: %v", err)
	}
	defer srv.Execute(context.Background(), json.RawMessage(`{"action":"stop","port":8932}`))

	cp := NewCheckPage(dir)
	expr := `{"eval":"JSON.stringify(window.__built||null)"`

	// file:// — the module never loads, so the state is absent.
	viaPath, err := cp.Execute(context.Background(), json.RawMessage(expr+`,"path":"index.html"}`))
	if err != nil {
		t.Fatalf("path eval: %v", err)
	}
	if strings.Contains(viaPath, `"blades":5`) {
		t.Fatalf("file:// should NOT reach module state, got: %s", viaPath)
	}

	// http:// — same page, same expression, real answer.
	viaURL, err := cp.Execute(context.Background(), json.RawMessage(expr+`,"url":"http://127.0.0.1:8932/index.html"}`))
	if err != nil {
		t.Fatalf("url eval: %v", err)
	}
	if !strings.Contains(viaURL, `"blades":5`) {
		t.Fatalf("eval over a served URL must reach module state, got: %s", viaURL)
	}
	// The report must name the real page, never the temp harness copy.
	if strings.Contains(viaURL, ".crv-eval-") {
		t.Errorf("temp harness name leaked into the report: %s", viaURL)
	}
	// And it must clean up after itself.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".crv-eval-") {
			t.Errorf("harness copy left behind: %s", e.Name())
		}
	}
}
