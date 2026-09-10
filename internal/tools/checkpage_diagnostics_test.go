package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func diagnosticChrome(t *testing.T, script string) *CheckPage {
	t.Helper()
	dir := t.TempDir()
	chrome := filepath.Join(dir, "browser")
	if err := os.WriteFile(chrome, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html><body>fixture</body></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRV_CHROME", chrome)
	return NewCheckPage(dir)
}

func TestCheckPageReportsProcessFailureNotCleanLoad(t *testing.T) {
	cp := diagnosticChrome(t, "echo 'browser launch failure' >&2\nexit 7\n")
	out, err := cp.Execute(context.Background(), json.RawMessage(`{"path":"index.html","eval":"1+1"}`))
	if err == nil || !strings.Contains(out, "process_failed") || !strings.Contains(out, "browser launch failure") || strings.Contains(out, "loaded cleanly") {
		t.Fatalf("process failure became a clean page: out=%s err=%v", out, err)
	}
}

func TestCheckPageTimeoutRetainsPartialEvidenceWithoutPass(t *testing.T) {
	cp := diagnosticChrome(t, "echo '__CRV_STAGE__ harness_installed' >&2\necho '__CRV_EVAL__ true' >&2\nprintf '<html><body>partial</body></html>'\nsleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	out, err := cp.Execute(ctx, json.RawMessage(`{"path":"index.html","eval":"true"}`))
	if time.Since(started) > 3*time.Second {
		t.Fatal("browser descendants kept the timeout call alive")
	}
	if err == nil || !strings.Contains(out, `"status":"timeout"`) || !strings.Contains(out, "harness_installed") || strings.Contains(out, "loaded cleanly") {
		t.Fatalf("timeout disappeared or lost evidence: out=%s err=%v", out, err)
	}
}

func TestCheckPageRealBrowserStagesAndWebGL(t *testing.T) {
	if findChrome() == "" {
		t.Skip("no headless browser; real browser test unverified")
	}
	dir := t.TempDir()
	page := `<!doctype html><html><body><canvas id="canvas"></canvas><div id="hud"></div><script>
	window.__fixture = {webgl: !!document.getElementById('canvas').getContext('webgl2')};
	document.getElementById('hud').textContent = 'fixture initialized';
	</script></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := NewCheckPage(dir).Execute(context.Background(), json.RawMessage(`{"path":"index.html","eval":"window.__fixture.webgl && document.getElementById('hud').textContent === 'fixture initialized'"}`))
	d, ok := ParseBrowserDiagnostics(out)
	if err != nil || !ok || d.Status != "completed" || !d.DOMObserved || !d.EvalObserved || d.LastStage != "eval_finished" || !strings.Contains(out, "eval result: true") {
		t.Fatalf("real browser fixture did not verify: %v\n%s", err, out)
	}
	for _, stage := range []string{"harness_installed", "dom_content_loaded", "window_loaded", "eval_started", "eval_finished"} {
		if !strings.Contains(out, stage) {
			t.Fatalf("missing loading stage %s: %s", stage, out)
		}
	}
	t.Log(out)
}

func TestCheckPageRealBrowserBlockedThreadIsNotCleanLoad(t *testing.T) {
	if findChrome() == "" {
		t.Skip("no headless browser; real browser test unverified")
	}
	dir := t.TempDir()
	// A classic script announces startup, then starves the same page thread
	// used by the injected eval. File mode suffices; no benchmark file touched.
	page := `<!doctype html><html><body><script>
	console.log('fixture blocking main thread'); while (true) {}
	</script></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	started := time.Now()
	out, err := NewCheckPage(dir).Execute(ctx, json.RawMessage(`{"path":"index.html","eval":"1+1"}`))
	d, ok := ParseBrowserDiagnostics(out)
	if err == nil || !ok || d.Status != "timeout" || d.EvalObserved || strings.Contains(out, "loaded cleanly") || !strings.Contains(out, "fixture blocking main thread") {
		t.Fatalf("blocked real browser was misreported: %v\n%s", err, out)
	}
	if time.Since(started) > 6*time.Second {
		t.Fatal("blocked browser exceeded bounded cleanup")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".crv-eval-*.html"))
	if len(leftovers) != 0 {
		t.Fatalf("instrumented copies retained after timeout: %v", leftovers)
	}
	t.Log(out)
}

func TestCheckPageRealBrowserPromiseTimeoutIsIncomplete(t *testing.T) {
	if findChrome() == "" {
		t.Skip("no headless browser; real browser test unverified")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html><body>promise fixture</body></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := NewCheckPage(dir).Execute(context.Background(), json.RawMessage(`{"path":"index.html","eval":"new Promise(() => {})"}`))
	d, ok := ParseBrowserDiagnostics(out)
	if err == nil || !ok || d.Status != "eval_timeout" || !d.DOMObserved || !strings.Contains(out, "promise did not settle") {
		t.Fatalf("promise timeout treated as a completed assertion: %v\n%s", err, out)
	}
}

func TestCheckPageNoDOMFailsClosed(t *testing.T) {
	cp := diagnosticChrome(t, "exit 0\n")
	out, err := cp.Execute(context.Background(), json.RawMessage(`{"path":"index.html"}`))
	if err == nil || !strings.Contains(out, "missing_dom") || strings.Contains(out, "loaded cleanly") {
		t.Fatalf("missing DOM reported as success: out=%s err=%v", out, err)
	}
}

func TestCheckPageMissingEvalDoesNotBlameThrownExpression(t *testing.T) {
	cp := diagnosticChrome(t, "printf '<html><body>loaded</body></html>'\n")
	out, err := cp.Execute(context.Background(), json.RawMessage(`{"path":"index.html","eval":"1+1"}`))
	if err == nil || !strings.Contains(out, "missing_eval") || strings.Contains(out, "expression may have thrown") || strings.Contains(out, "loaded cleanly") {
		t.Fatalf("missing evidence misclassified: out=%s err=%v", out, err)
	}
}
