package tools

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Trusted runtime ships inside the same host binary as the tool schemas.
//
//go:embed browsernative/engine.mjs browsernative/runner.mjs
var browserNativeRuntime embed.FS

type BrowserNative struct{ workspace, action string }

func NewBrowserRun(workspace string) *BrowserNative {
	return &BrowserNative{newJail(workspace).root, "browser_run"}
}
func NewRuntimeProfile(workspace string) *BrowserNative {
	return &BrowserNative{newJail(workspace).root, "runtime_profile"}
}
func (t *BrowserNative) Name() string { return t.action }
func (t *BrowserNative) Description() string {
	if t.action == "runtime_profile" {
		return "Profile a local application's startup for 1–5 seconds: bounded CPU hotspots with source locations, long tasks, frame/resource timing where observable. Existing Chromium/Playwright only. Missing metrics are unavailable, not zero; a profile is not an application pass. Fresh isolated browser, exact local GET/HEAD origin only; no network mutation, credentials or caller code. Retains .devcheck receipts."
	}
	return "Run 1–20 typed real browser actions in ONE fresh local-page session: click, fill, key, pointer, condition wait, DOM check and screenshot. State persists between steps, not between calls. Use checks to prove observed outcomes; actions alone are not acceptance. At most six captures. Exact target origin GET/HEAD only: POST, WebSockets, remote assets, credentials, downloads and popup flows are blocked and reported. No arbitrary eval or caller scripts. Existing Playwright/Chromium required, never installed automatically. Retains step results and .devcheck evidence."
}

func nativeSchemaString(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func nativeSchemaInt(lo, hi int) map[string]any {
	return map[string]any{"type": "integer", "minimum": lo, "maximum": hi}
}
func nativeSchemaEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
func (t *BrowserNative) Schema() map[string]any {
	props := map[string]any{"url": nativeSchemaString("Explicit http://localhost:PORT or http://127.0.0.1:PORT local app URL, port1024–65535")}
	required := []string{"url"}
	if t.action == "runtime_profile" {
		props["duration_ms"] = nativeSchemaInt(1000, 5000)
		props["sampling_interval_us"] = nativeSchemaInt(1000, 10000)
		props["max_hotspots"] = nativeSchemaInt(1, 20)
	} else {
		condition := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"selector", "kind", "expected"}, "properties": map[string]any{
			"selector": nativeSchemaString("Unambiguous CSS selector, no XPath/selector engines"), "kind": nativeSchemaEnum("exists", "visible", "text", "count"), "expected": map[string]any{"description": "boolean for exists/visible; exact string for text; integer for count", "anyOf": []any{map[string]any{"type": "boolean"}, map[string]any{"type": "string"}, map[string]any{"type": "integer"}}},
		}}
		props["wait_ms"] = nativeSchemaInt(0, 1500)
		props["max_bytes"] = nativeSchemaInt(16384, 262144)
		props["steps"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"type"}, "properties": map[string]any{
			"id": nativeSchemaString("Unique step identity up to40 letters/digits/underscore/hyphen"), "type": nativeSchemaEnum("click", "fill", "key", "pointer", "wait", "check", "capture"),
			"selector": nativeSchemaString("CSS target for click/fill"), "button": nativeSchemaEnum("left", "middle", "right"), "value": nativeSchemaString("Fill value, max2000characters"),
			"key":    nativeSchemaString("Single printable ASCII key or named Enter/Tab/Escape/Space/ArrowUp/ArrowDown/ArrowLeft/ArrowRight/Shift/Control/Alt"),
			"action": nativeSchemaEnum("press", "hold", "release", "move", "drag"), "hold_ms": nativeSchemaInt(0, 1000), "duration_ms": nativeSchemaInt(0, 1000),
			"x": nativeSchemaInt(0, 1279), "y": nativeSchemaInt(0, 959), "from_x": nativeSchemaInt(0, 1279), "from_y": nativeSchemaInt(0, 959),
			"condition": condition, "timeout_ms": nativeSchemaInt(100, 5000), "capture": nativeSchemaEnum("before", "after", "both"),
		}}}
		required = append(required, "steps")
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
}

var browserNativeURL = regexp.MustCompile(`^http://(?:localhost|127\.0\.0\.1):[0-9]{4,5}(?:[/?]|$)`)

func validateBrowserNativeURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !browserNativeURL.MatchString(raw) || len(raw) > 2048 || u.User != nil || u.Fragment != "" {
		return errors.New("URL_SCOPE: explicit local HTTP origin required, no credentials or fragments")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1024 || port > 65535 {
		return errors.New("URL_SCOPE: port must be1024..65535")
	}
	return nil
}

func (t *BrowserNative) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	failure := func(code, message string) (string, error) {
		raw, _ := json.Marshal(map[string]any{"schema": "cerveau.browsernative.v1", "action": t.action, "ok": false, "verdict": "unverified", "error": map[string]string{"code": code, "message": message}})
		return string(raw), errors.New(message)
	}
	if len(args) > 32<<10 {
		return failure("INVALID_INPUT", "input exceeds32KiB")
	}
	var input map[string]any
	if err := json.Unmarshal(args, &input); err != nil || input == nil {
		return failure("INVALID_INPUT", "input must be one JSON object")
	}
	if err := validateReflexArgs(t.Schema(), input); err != nil {
		return failure("INVALID_INPUT", err.Error())
	}
	rawURL, _ := input["url"].(string)
	if err := validateBrowserNativeURL(rawURL); err != nil {
		return failure("URL_SCOPE", err.Error())
	}
	module := os.Getenv("CERVEAU_PLAYWRIGHT_MODULE")
	if !filepath.IsAbs(module) {
		return failure("DEPENDENCY_MISSING", "configure CERVEAU_PLAYWRIGHT_MODULE with an already-installed absolute Playwright module path; no automatic installation")
	}
	if info, err := os.Stat(module); err != nil || !info.Mode().IsRegular() {
		return failure("DEPENDENCY_MISSING", "configured Playwright module is unavailable")
	}
	chrome := os.Getenv("CERVEAU_CHROMIUM")
	if chrome == "" {
		chrome = findChrome()
	}
	if chrome == "" {
		return failure("DEPENDENCY_MISSING", "no installed Chromium available")
	}
	root, err := filepath.EvalSymlinks(t.workspace)
	if err != nil || root == "/" {
		return failure("WORKSPACE_SCOPE", "workspace must be an existing scoped directory")
	}
	directory, err := os.MkdirTemp("", "crv-browsernative-")
	if err != nil {
		return failure("RUNTIME_FAILED", err.Error())
	}
	defer os.RemoveAll(directory) // exact directory created above; no caller path
	for _, name := range []string{"engine.mjs", "runner.mjs"} {
		data, err := browserNativeRuntime.ReadFile("browsernative/" + name)
		if err != nil {
			return failure("RUNTIME_FAILED", err.Error())
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			return failure("RUNTIME_FAILED", err.Error())
		}
	}
	payload, _ := json.Marshal(map[string]any{"workspace": root, "input": input})
	ctx = context.WithValue(ctx, nativeProcessReadPathsKey{}, []string{directory})
	ctx = context.WithValue(ctx, nativeProcessEnvironmentKey{}, []string{"CERVEAU_PLAYWRIGHT_MODULE=" + module, "CERVEAU_CHROMIUM=" + chrome})
	r := runNativeProcess(ctx, root, "node", []string{filepath.Join(directory, "runner.mjs"), t.action}, payload, 40*time.Second, false)
	var result struct {
		Schema    string                  `json:"schema"`
		Action    string                  `json:"action"`
		OK        bool                    `json:"ok"`
		Verdict   string                  `json:"verdict"`
		Evidence  nativeBrowserArtifact   `json:"evidence"`
		Artifacts []nativeBrowserArtifact `json:"artifacts"`
	}
	if r.TimedOut || ctx.Err() != nil || r.StdoutTruncated || json.Unmarshal([]byte(r.Stdout), &result) != nil || result.Schema != "cerveau.browsernative.v1" || result.Action != t.action || (r.ExitCode != 0 && r.ExitCode != 1) {
		return failure("PROCESS_UNAVAILABLE", fmt.Sprintf("browser procedure did not return complete evidence (exit %d, timeout %t): %s %s", r.ExitCode, r.TimedOut, CapIngress(r.Stderr, 1500), CapIngress(fmt.Sprint(r.Err), 500)))
	}
	if result.Evidence.Path != "" {
		for _, artifact := range append([]nativeBrowserArtifact{result.Evidence}, result.Artifacts...) {
			if err := validateNativeBrowserArtifact(root, artifact); err != nil {
				return failure("ARTIFACT_INVALID", err.Error())
			}
		}
	} else if result.OK {
		return failure("ARTIFACT_MISSING", "successful browser procedure has no retained receipt")
	}
	validVerdict := t.action == "runtime_profile" && result.Verdict == "profiled" || t.action == "browser_run" && (result.Verdict == "passed" || result.Verdict == "executed")
	if result.OK && (r.Err != nil || r.ExitCode != 0 || !validVerdict) {
		return failure("RESULT_MISMATCH", "browser result conflicts with process outcome")
	}
	if !result.OK {
		return strings.TrimSpace(r.Stdout), fmt.Errorf("%s %s; inspect retained evidence", t.action, result.Verdict)
	}
	return strings.TrimSpace(r.Stdout), nil
}

type nativeBrowserArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

func validateNativeBrowserArtifact(workspace string, a nativeBrowserArtifact) error {
	if !strings.HasPrefix(a.Path, ".devcheck/") || filepath.Clean(a.Path) != a.Path || filepath.IsAbs(a.Path) || strings.ContainsAny(a.Path, "\\\x00") || a.Bytes < 1 || a.Bytes > 4<<20 {
		return errors.New("invalid browser artifact path or bound")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	// Refuse every symlink component, not just escapes; a receipt binds bytes
	// at the actual path the model/user can retrieve later.
	part := ""
	for _, segment := range strings.Split(a.Path, "/") {
		part = filepath.Join(part, segment)
		info, err := root.Lstat(part)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("browser artifact contains missing/symlink component")
		}
	}
	f, err := root.Open(a.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != a.Bytes {
		return errors.New("browser artifact size/type changed")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, 4<<20+1))
	if err != nil || n != a.Bytes || fmt.Sprintf("%x", h.Sum(nil)) != a.SHA256 {
		return errors.New("browser artifact hash changed")
	}
	return nil
}
