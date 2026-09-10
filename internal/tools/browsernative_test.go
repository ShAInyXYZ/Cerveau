package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeBrowserTestRuntime(t *testing.T) {
	t.Helper()
	module := os.Getenv("CERVEAU_PLAYWRIGHT_MODULE")
	if module == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		module = filepath.Join(home, ".cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright/index.mjs")
	}
	if _, err := os.Stat(module); err != nil {
		t.Skip("existing Playwright module unavailable; browser bridge integration unverified")
	}
	chrome := os.Getenv("CERVEAU_CHROMIUM")
	if chrome == "" {
		chrome = findChrome()
	}
	if chrome == "" {
		t.Skip("existing Chromium unavailable; browser bridge integration unverified")
	}
	t.Setenv("CERVEAU_PLAYWRIGHT_MODULE", module)
	t.Setenv("CERVEAU_CHROMIUM", chrome)
	requireNativeSandbox(t)
}

func TestBrowserNativeRejectsURLsAndCode(t *testing.T) {
	tool := NewBrowserRun(t.TempDir())
	for _, input := range []string{
		`{"url":"http://169.254.169.254:8000","steps":[{"type":"capture"}]}`,
		`{"url":"http://127.1:8000","steps":[{"type":"capture"}]}`,
		`{"url":"http://localhost:8000","steps":[],"eval":"1+1"}`,
		`{"url":"http://localhost:8000","steps":[{"type":"capture"}],"workspace":"/"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(input)); err == nil {
			t.Fatalf("accepted out-of-scope input %s", input)
		}
	}
}

func TestBrowserNativeArtifactIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".devcheck"), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("receipt")
	path := filepath.Join(dir, ".devcheck", "evidence.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	a := nativeBrowserArtifact{Path: ".devcheck/evidence.json", Bytes: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	if err := validateNativeBrowserArtifact(dir, a); err != nil {
		t.Fatal(err)
	}
	a.SHA256 = "bad"
	if validateNativeBrowserArtifact(dir, a) == nil {
		t.Fatal("accepted changed bytes")
	}
	if err := os.Symlink(path, filepath.Join(dir, ".devcheck", "link")); err != nil {
		t.Fatal(err)
	}
	a.Path = ".devcheck/link"
	if validateNativeBrowserArtifact(dir, a) == nil {
		t.Fatal("accepted artifact symlink")
	}
}

func TestBrowserRunNativeBridgeRealSequence(t *testing.T) {
	nativeBrowserTestRuntime(t)
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body><input id="name"><button id="add">Add</button><output id="result">0</output><script>
		document.querySelector('#add').onclick=()=>document.querySelector('#result').textContent=document.querySelector('#name').value;
		</script></body></html>`)
	}))
	defer server.Close()
	input := map[string]any{"url": server.URL, "steps": []any{
		map[string]any{"type": "fill", "selector": "#name", "value": "native sequence"},
		map[string]any{"type": "click", "selector": "#add"},
		map[string]any{"type": "check", "condition": map[string]any{"selector": "#result", "kind": "text", "expected": "native sequence"}},
		map[string]any{"type": "capture"},
	}}
	raw, _ := json.Marshal(input)
	out, err := NewBrowserRun(dir).Execute(context.Background(), raw)
	if err != nil || !strings.Contains(out, `"verdict":"passed"`) {
		t.Fatalf("sequence failed: %v\n%s", err, out)
	}
	var result struct {
		Evidence  nativeBrowserArtifact   `json:"evidence"`
		Artifacts []nativeBrowserArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("missing capture receipt: %s", out)
	}
	if err := validateNativeBrowserArtifact(dir, result.Evidence); err != nil {
		t.Fatal(err)
	}
	t.Log(out)
	input["steps"] = []any{map[string]any{"type": "check", "condition": map[string]any{"selector": "#result", "kind": "text", "expected": "native sequence"}}}
	raw, _ = json.Marshal(input)
	out, err = NewBrowserRun(dir).Execute(context.Background(), raw)
	if err == nil || !strings.Contains(out, `"verdict":"failed"`) {
		t.Fatalf("fresh page silently reused prior state: %v %s", err, out)
	}
}

func TestRuntimeProfileNativeBridge(t *testing.T) {
	nativeBrowserTestRuntime(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body>profile fixture<script>function busyFixture(){let end=performance.now()+150;while(performance.now()<end){Math.sqrt(Math.random());}} busyFixture();</script></body></html>`)
	}))
	defer server.Close()
	raw, _ := json.Marshal(map[string]any{"url": server.URL, "duration_ms": 1000})
	out, err := NewRuntimeProfile(t.TempDir()).Execute(context.Background(), raw)
	if err != nil || !strings.Contains(out, `"verdict":"profiled"`) {
		t.Fatalf("profile unavailable: %v\n%s", err, out)
	}
	t.Log(out)
}
