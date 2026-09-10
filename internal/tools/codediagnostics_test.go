package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func diagnosticWriteFixture(t *testing.T, root, path, source string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func diagnosticExecuteFixture(t *testing.T, tool *CodeDiagnostics, args string) diagnosticReport {
	t.Helper()
	raw, err := tool.Execute(context.Background(), json.RawMessage(args))
	if len(raw) > 8000 {
		t.Fatalf("model-facing diagnostics exceed 8000 bytes: %d", len(raw))
	}
	var got diagnosticReport
	if decodeErr := json.Unmarshal([]byte(raw), &got); decodeErr != nil {
		t.Fatalf("invalid report: %v: %s (execute error: %v)", decodeErr, raw, err)
	}
	if (got.Status == "pass") != (err == nil) {
		t.Fatalf("tool error must match non-pass status: status=%s error=%v", got.Status, err)
	}
	var receipt struct {
		Path string `json:"receipt_path"`
		SHA  string `json:"receipt_sha256"`
	}
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil || receipt.Path == "" || receipt.SHA == "" {
		t.Fatalf("missing receipt identity: %v: %s", err, raw)
	}
	full, err := tool.j.resolve(receipt.Path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(full)
	if err != nil || diagnosticSHA(data) != receipt.SHA {
		t.Fatalf("receipt hash mismatch: %v", err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid retained evidence: %v", err)
	}
	return got
}

func diagnosticFakeTool(root string, result nativeProcessResult) *CodeDiagnostics {
	tool := NewCodeDiagnostics(root)
	tool.lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		return result
	}
	return tool
}

func TestCodeDiagnosticsRejectsInvalidRequestsBeforeExecution(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "valid.js", "export const answer = 42;\n")
	diagnosticWriteFixture(t, root, "valid.ts", "const answer: number = 42;\n")
	outside := t.TempDir()
	diagnosticWriteFixture(t, outside, "outside.js", "const answer = 42;\n")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	tool := diagnosticFakeTool(root, nativeProcessResult{})
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		t.Fatal("invalid request executed a process")
		return nativeProcessResult{}
	}
	for _, args := range []string{
		`{}`, `null`, `{"checker":"node","paths":[]}`,
		`{"checker":"shell","paths":["valid.js"]}`,
		`{"checker":"node","paths":["../outside.js"]}`,
		`{"checker":"node","paths":["/etc/passwd"]}`,
		`{"checker":"node","paths":["escape/outside.js"]}`,
		`{"checker":"node","paths":["--help.js"]}`,
		`{"checker":"node","paths":["@args.js"]}`,
		`{"checker":"node","paths":["valid.js; touch pwn"]}`,
		`{"checker":"node","paths":["sub/../valid.js"]}`,
		`{"checker":"node","paths":["valid.js","./valid.js"]}`,
		`{"checker":"node","paths":["valid.ts"]}`,
		`{"checker":"node","paths":["valid.js"],"timeout_ms":0}`,
		`{"checker":"node","paths":["valid.js"],"timeout_ms":null}`,
		`{"checker":"node","paths":["valid.js"],"timeout_ms":60001}`,
		`{"checker":"node","paths":["valid.js"],"js_mode":"auto"}`,
		`{"checker":"node","paths":["valid.js"],"js_mode":""}`,
		`{"checker":"node","paths":["valid.js"],"js_mode":null}`,
		`{"checker":"typescript","paths":["valid.ts"],"js_mode":"module"}`,
		`{"checker":"node","paths":["valid.js"],"command":"touch pwn"}`,
		`{"checker":"node","paths":["valid.js"],"env":{"NODE_OPTIONS":"--require evil.js"}}`,
		`{"checker":"node","paths":["valid.js"]} {}`,
	} {
		t.Run(args, func(t *testing.T) {
			if _, err := tool.Execute(context.Background(), json.RawMessage(args)); err == nil {
				t.Fatalf("accepted invalid args: %s", args)
			}
		})
	}
}

func TestCodeDiagnosticsNodeUsesFixedArgsStdinAndReadOnlyRunner(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"first.js", "module.mjs", "legacy.cjs"} {
		diagnosticWriteFixture(t, root, file, "// "+file+"\n")
	}
	tool := diagnosticFakeTool(root, nativeProcessResult{})
	var invocations int
	tool.run = func(ctx context.Context, workspace, executable string, args []string, stdin []byte, timeout time.Duration, readOnly bool) nativeProcessResult {
		invocations++
		if !readOnly || workspace != root || executable != "/usr/bin/node" || timeout > 2*time.Second || len(args) != 2 || args[0] != "--check" {
			t.Fatalf("unsafe invocation: workspace=%s executable=%s args=%v timeout=%v readOnly=%v", workspace, executable, args, timeout, readOnly)
		}
		wantMode := "module"
		if strings.Contains(string(stdin), "legacy.cjs") {
			wantMode = "commonjs"
		}
		if args[1] != "--input-type="+wantMode {
			t.Fatalf("mode %v for %s", args, stdin)
		}
		return nativeProcessResult{ExitCode: 0}
	}
	got := diagnosticExecuteFixture(t, tool, `{"checker":"node","paths":["first.js","module.mjs","legacy.cjs"],"timeout_ms":2000}`)
	if got.Status != "pass" || !got.Complete || invocations != 3 || len(got.Sources) != 3 || len(got.Checks) != 3 || got.CheckID == "" {
		t.Fatalf("unexpected report: %+v", got)
	}
	for _, source := range got.Sources {
		if len(source.SHA256Before) != 64 || source.SHA256Before != source.SHA256After || !source.Unchanged {
			t.Fatalf("invalid source evidence: %+v", source)
		}
	}
}

func TestCodeDiagnosticsFailClosedResultStates(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "bad.js", "const value = ;\n")
	const syntax = "[stdin]:1\nconst value = ;\n              ^\n\nSyntaxError: Unexpected token ';'\n    at checkSyntax (node:internal/main/check_syntax:74:5)\n\nNode.js v22.23.2\n"
	for _, test := range []struct {
		name     string
		result   nativeProcessResult
		status   string
		complete bool
	}{
		{"clean execution", nativeProcessResult{ExitCode: 0}, "pass", true},
		{"parsed syntax", nativeProcessResult{ExitCode: 1, Err: errors.New("exit status 1"), Stderr: syntax}, "fail", true},
		{"diagnostic despite zero", nativeProcessResult{ExitCode: 0, Stderr: syntax}, "fail", true},
		{"empty nonzero", nativeProcessResult{ExitCode: 2, Err: errors.New("exit status 2")}, "unverified", false},
		{"startup failure", nativeProcessResult{ExitCode: -1, Err: errors.New("sandbox unavailable")}, "unverified", false},
		{"bwrap setup failure", nativeProcessResult{ExitCode: 1, Err: errors.New("exit status 1"), Stderr: "bwrap: Creating new namespace failed: Operation not permitted\n"}, "unverified", false},
		{"timeout", nativeProcessResult{ExitCode: -1, TimedOut: true, Stderr: syntax}, "unverified", false},
		{"canceled runner", nativeProcessResult{ExitCode: 1, Err: context.Canceled, Stderr: syntax}, "unverified", false},
		{"unsupported output", nativeProcessResult{ExitCode: 0, Stderr: "unexpected runtime warning\n"}, "unverified", false},
		{"partial parse", nativeProcessResult{ExitCode: 1, Stderr: syntax + "unrecognized final error\n"}, "unverified", false},
		{"truncated stdout", nativeProcessResult{ExitCode: 0, StdoutTruncated: true}, "unverified", false},
		{"truncated stderr", nativeProcessResult{ExitCode: 1, Stderr: syntax, StderrTruncated: true}, "unverified", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := diagnosticExecuteFixture(t, diagnosticFakeTool(root, test.result), `{"checker":"node","paths":["bad.js"]}`)
			if got.Status != test.status || got.Complete != test.complete {
				t.Fatalf("got status=%s complete=%v, want %s %v: %+v", got.Status, got.Complete, test.status, test.complete, got)
			}
		})
	}
}

func TestCodeDiagnosticsSourceChangeInvalidatesCleanResult(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "source.js", "const value = 1;\n")
	tool := diagnosticFakeTool(root, nativeProcessResult{})
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		diagnosticWriteFixture(t, root, "source.js", "const value = ;\n")
		return nativeProcessResult{ExitCode: 0}
	}
	got := diagnosticExecuteFixture(t, tool, `{"checker":"node","paths":["source.js"]}`)
	if got.Status != "unverified" || got.Complete || got.Sources[0].Unchanged || got.Sources[0].SHA256Before == got.Sources[0].SHA256After {
		t.Fatalf("stale source passed: %+v", got)
	}
}

func TestCodeDiagnosticsMissingAndOversizedSourcesFailClosed(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "large.js", strings.Repeat(" ", diagnosticMaxSourceBytes+1))
	diagnosticWriteFixture(t, root, "source.js", "const value = 1;\n")
	tool := diagnosticFakeTool(root, nativeProcessResult{})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"checker":"node","paths":["large.js"]}`)); err == nil {
		t.Fatal("oversized source accepted")
	}
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		if err := os.Remove(filepath.Join(root, "source.js")); err != nil {
			t.Fatal(err)
		}
		return nativeProcessResult{ExitCode: 0}
	}
	got := diagnosticExecuteFixture(t, tool, `{"checker":"node","paths":["source.js"]}`)
	if got.Status != "unverified" || got.Complete || got.Sources[0].Unchanged || got.Sources[0].Error == "" {
		t.Fatalf("deleted source passed: %+v", got)
	}
}

func TestCodeDiagnosticsBoundsRetainedRawOutput(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "source.js", "const value = 1;\n")
	result := nativeProcessResult{ExitCode: 0, Stdout: strings.Repeat("x", 32<<10)}
	got := diagnosticExecuteFixture(t, diagnosticFakeTool(root, result), `{"checker":"node","paths":["source.js"]}`)
	if got.Status != "unverified" || got.Checks[0].Stdout != result.Stdout || got.Checks[0].RawOutputShortened {
		t.Fatalf("receipt discarded retained raw output or passed incomplete output: %+v", got)
	}
}

func TestCodeDiagnosticsLargeReportHasBoundedHonestSummaryAndExclusiveReceipt(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "source.ts", "const value: number = 'wrong';\n")
	diagnosticWriteFixture(t, root, "node_modules/typescript/bin/tsc", "// local fixture\n")
	var output strings.Builder
	for i := 1; i <= 60; i++ {
		output.WriteString("source.ts(1,7): error TS2322: ")
		output.WriteString(strings.Repeat("Long detail ", 25))
		output.WriteByte('\n')
	}
	tool := diagnosticFakeTool(root, nativeProcessResult{ExitCode: 2, Stdout: output.String()})
	var previousPath string
	var previousData []byte
	for call := 0; call < 2; call++ {
		raw, err := tool.Execute(context.Background(), json.RawMessage(`{"checker":"typescript","paths":["source.ts"]}`))
		if err == nil || len(raw) > 8000 || !json.Valid([]byte(raw)) {
			t.Fatalf("failed or oversized model result: bytes=%d error=%v", len(raw), err)
		}
		if len(strings.TrimSpace(raw+"\n"+err.Error())) > 8000 {
			t.Fatal("summary plus the execution loop's appended error exceeds 8000 bytes")
		}
		var summary struct {
			Status              string              `json:"status"`
			ReceiptPath         string              `json:"receipt_path"`
			ReceiptSHA          string              `json:"receipt_sha256"`
			ReceiptBytes        int                 `json:"receipt_bytes"`
			DiagnosticsRecorded int                 `json:"diagnostics_recorded"`
			DiagnosticsOmitted  int                 `json:"diagnostics_omitted"`
			Diagnostics         []diagnosticFinding `json:"diagnostics"`
		}
		if err := json.Unmarshal([]byte(raw), &summary); err != nil {
			t.Fatal(err)
		}
		if summary.Status != "fail" || summary.DiagnosticsRecorded != 60 || summary.DiagnosticsOmitted == 0 || len(summary.Diagnostics)+summary.DiagnosticsOmitted != 60 {
			t.Fatalf("summary silently omitted diagnostic claims: %+v", summary)
		}
		if !strings.HasPrefix(summary.ReceiptPath, ".devcheck/code-diagnostics/") || summary.ReceiptPath == previousPath {
			t.Fatalf("receipt was not exclusive: %s, previous %s", summary.ReceiptPath, previousPath)
		}
		data, err := os.ReadFile(filepath.Join(root, summary.ReceiptPath))
		if err != nil || len(data) != summary.ReceiptBytes || diagnosticSHA(data) != summary.ReceiptSHA {
			t.Fatalf("retained evidence identity mismatch: %v", err)
		}
		var receipt diagnosticReport
		if err := json.Unmarshal(data, &receipt); err != nil || len(receipt.Diagnostics) != 60 || len(receipt.Sources) != 1 || receipt.Checks[0].Stdout != output.String() {
			t.Fatalf("receipt discarded full evidence: %v", err)
		}
		if previousPath != "" {
			unchanged, err := os.ReadFile(filepath.Join(root, previousPath))
			if err != nil || string(unchanged) != string(previousData) {
				t.Fatalf("prior receipt was overwritten: %v", err)
			}
		}
		previousPath, previousData = summary.ReceiptPath, data
	}
}

func TestCodeDiagnosticsReceiptFailureCannotPassOrEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	diagnosticWriteFixture(t, root, "source.js", "const value = 1;\n")
	if err := os.Symlink(outside, filepath.Join(root, ".devcheck")); err != nil {
		t.Fatal(err)
	}
	raw, err := diagnosticFakeTool(root, nativeProcessResult{ExitCode: 0}).Execute(context.Background(), json.RawMessage(`{"checker":"node","paths":["source.js"]}`))
	if err == nil || len(raw) > 8000 {
		t.Fatalf("receipt write failure was not bounded and failed closed: %v", err)
	}
	var report struct {
		Status       string `json:"status"`
		Complete     bool   `json:"complete"`
		ReceiptSaved bool   `json:"receipt_saved"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil || report.Status != "unverified" || report.Complete || report.ReceiptSaved {
		t.Fatalf("receipt failure claimed verified evidence: %v: %s", err, raw)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("receipt escaped workspace: %v %v", entries, err)
	}
}

func TestCodeDiagnosticsCancellationPreventsExecution(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "source.js", "const value = 1;\n")
	tool := diagnosticFakeTool(root, nativeProcessResult{})
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		t.Fatal("canceled request executed checker")
		return nativeProcessResult{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, err := tool.Execute(ctx, json.RawMessage(`{"checker":"node","paths":["source.js"]}`))
	if err == nil {
		t.Fatal("canceled request did not return tool error")
	}
	var got diagnosticReport
	if err := json.Unmarshal([]byte(raw), &got); err != nil || got.Status != "unverified" || got.Complete {
		t.Fatalf("canceled check passed: err=%v result=%s", err, raw)
	}
}

func TestCodeDiagnosticsMissingRuntimeIsUnverified(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "source.js", "export const value = 1;\n")
	tool := diagnosticFakeTool(root, nativeProcessResult{})
	tool.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	got := diagnosticExecuteFixture(t, tool, `{"checker":"node","paths":["source.js"]}`)
	if got.Status != "unverified" || got.Complete || len(got.Checks) != 0 || !strings.Contains(got.Reason, "node") {
		t.Fatalf("missing runtime passed: %+v", got)
	}
}

func TestCodeDiagnosticsTypeScriptRequiresJailedInstalledCompiler(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "source.ts", "const value: number = 1;\n")
	tool := diagnosticFakeTool(root, nativeProcessResult{})
	got := diagnosticExecuteFixture(t, tool, `{"checker":"typescript","paths":["source.ts"]}`)
	if got.Status != "unverified" || len(got.Checks) != 0 {
		t.Fatalf("missing local compiler passed: %+v", got)
	}
	diagnosticWriteFixture(t, root, "node_modules/typescript/bin/tsc", "// installed compiler fixture\n")
	tool.run = func(_ context.Context, _ string, executable string, args []string, _ []byte, _ time.Duration, readOnly bool) nativeProcessResult {
		if !readOnly || executable != "/usr/bin/node" || len(args) < 5 || args[0] != filepath.Join(root, "node_modules/typescript/bin/tsc") || !strings.Contains(strings.Join(args, " "), "--noEmit --pretty false") || args[len(args)-1] != filepath.Join(root, "source.ts") {
			t.Fatalf("unsafe tsc command: %s %v", executable, args)
		}
		return nativeProcessResult{ExitCode: 2, Stdout: "source.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.\n"}
	}
	got = diagnosticExecuteFixture(t, tool, `{"checker":"typescript","paths":["source.ts"]}`)
	if got.Status != "fail" || !got.Complete || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "TS2322" {
		t.Fatalf("invalid tsc report: %+v", got)
	}
}

func TestCodeDiagnosticsTypeScriptCompilerSymlinkEscapeIsUnverified(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	diagnosticWriteFixture(t, root, "source.ts", "const value: number = 1;\n")
	diagnosticWriteFixture(t, outside, "bin/tsc", "// outside compiler\n")
	if err := os.Mkdir(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "node_modules/typescript")); err != nil {
		t.Fatal(err)
	}
	tool := diagnosticFakeTool(root, nativeProcessResult{})
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		t.Fatal("escaped compiler was executed")
		return nativeProcessResult{}
	}
	got := diagnosticExecuteFixture(t, tool, `{"checker":"typescript","paths":["source.ts"]}`)
	if got.Status != "unverified" || got.Complete {
		t.Fatalf("escaped compiler accepted: %+v", got)
	}
}

func TestCodeDiagnosticsParsers(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "src/app.ts", "const value: number = 'bad';\n")
	diagnosticWriteFixture(t, root, "main.go", "package main\n")
	j := newJail(root)
	for _, test := range []struct {
		name, checker, stdout, stderr string
		count                         int
		complete                      bool
	}{
		{"ts error", "typescript", "src/app.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.\n", "", 1, true},
		{"ts continuation", "typescript", "src/app.ts(1,7): error TS2322: Type is not assignable.\n  Property 'x' is missing.\n", "", 1, true},
		{"ts unknown", "typescript", "src/app.ts(1,7): error TS2322: Broken.\ncompiler crashed\n", "", 1, false},
		{"ts missing module", "typescript", "src/app.ts(1,7): error TS2307: Cannot find module 'missing' or its corresponding type declarations.\n", "", 1, false},
		{"ts config error", "typescript", "error TS5112: tsconfig.json is present but will not be loaded.\n", "", 1, false},
		{"ts outside", "typescript", "../outside.ts(1,1): error TS2322: Broken.\n", "", 0, false},
		{"ts huge position", "typescript", "src/app.ts(9999999999999999999999999,1): error TS2322: Broken.\n", "", 0, false},
		{"go json", "go_vet", "", "# command-line-arguments\n{\"command-line-arguments\":{\"printf\":[{\"posn\":\"" + filepath.Join(root, "main.go") + ":3:10\",\"message\":\"fmt.Printf format mismatch\"}]}}\n", 1, true},
		{"go empty json", "go_vet", "", "# command-line-arguments\n{}\n", 0, true},
		{"go fallback compile", "go_vet", "", "# command-line-arguments\nvet: ./main.go:3:10: undefined: answer\n", 1, true},
		{"go unknown", "go_vet", "", "# command-line-arguments\nbuild cache unavailable\n", 0, false},
		{"go malformed", "go_vet", "", "# command-line-arguments\n{\"command-line-arguments\":", 0, false},
		{"go unknown analyzer", "go_vet", "", "{\"command-line-arguments\":{\"printf\":{\"error\":\"analysis failed\"}}}\n", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostics, complete := diagnosticParse(j, test.checker, "src/app.ts", test.stdout, test.stderr)
			if len(diagnostics) != test.count || complete != test.complete {
				t.Fatalf("got %d complete=%v, want %d complete=%v: %+v", len(diagnostics), complete, test.count, test.complete, diagnostics)
			}
			for _, d := range diagnostics {
				if d.Message == "" || d.Severity != "error" || d.Check == "" {
					t.Fatalf("missing diagnostic identity: %+v", d)
				}
			}
		})
	}
}

func TestCodeDiagnosticsGoVetFixedFileArguments(t *testing.T) {
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "one.go", "package fixture\n")
	diagnosticWriteFixture(t, root, "two.go", "package fixture\n")
	tool := diagnosticFakeTool(root, nativeProcessResult{})
	tool.run = func(_ context.Context, _ string, executable string, args []string, _ []byte, _ time.Duration, readOnly bool) nativeProcessResult {
		if !readOnly || executable != "/usr/bin/go" || len(args) != 6 || strings.Join(args[:4], " ") != "vet -json -mod=readonly -p=1" || args[4] != filepath.Join(root, "one.go") || args[5] != filepath.Join(root, "two.go") {
			t.Fatalf("unsafe go vet command: %s %v", executable, args)
		}
		return nativeProcessResult{ExitCode: 0, Stderr: "{\"command-line-arguments\":{\"printf\":[{\"posn\":\"one.go:1:1\",\"message\":\"format mismatch\"}]}}\n"}
	}
	got := diagnosticExecuteFixture(t, tool, `{"checker":"go_vet","paths":["one.go","two.go"]}`)
	if got.Status != "fail" || !got.Complete || got.Diagnostics[0].Check != "go_vet/printf" {
		t.Fatalf("go vet finding with exit zero must fail: %+v", got)
	}
}

func TestCodeDiagnosticsNodeInstalledRuntimeIntegration(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("read-only native sandbox not installed")
	}
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "module.js", "import missing from './not-installed.js'; export const answer = 42;\n")
	diagnosticWriteFixture(t, root, "broken.js", "export const answer = ;\n")
	diagnosticWriteFixture(t, root, "noexec.cjs", "require('node:fs').writeFileSync('must-not-exist', 'bad');\n")
	clean := diagnosticExecuteFixture(t, NewCodeDiagnostics(root), `{"checker":"node","paths":["module.js","noexec.cjs"]}`)
	if clean.Status == "unverified" && len(clean.Checks) > 0 && strings.Contains(clean.Checks[0].Stderr, "bwrap:") {
		t.Skipf("native sandbox unavailable: %s", clean.Checks[0].Stderr)
	}
	if clean.Status != "pass" || !clean.Complete {
		t.Fatalf("valid ESM/CJS syntax did not pass: %+v", clean)
	}
	broken := diagnosticExecuteFixture(t, NewCodeDiagnostics(root), `{"checker":"node","paths":["broken.js"]}`)
	if broken.Status != "fail" || len(broken.Diagnostics) != 1 || broken.Diagnostics[0].File != "broken.js" || broken.Diagnostics[0].Line != 1 || broken.Diagnostics[0].Column <= 0 {
		t.Fatalf("invalid syntax not located: %+v", broken)
	}
	if _, err := os.Stat(filepath.Join(root, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("node --check executed source: %v", err)
	}
}

func TestCodeDiagnosticsGoInstalledRuntimeIntegration(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("read-only native sandbox not installed")
	}
	root := t.TempDir()
	diagnosticWriteFixture(t, root, "go.mod", "module diagnosticsfixture\n\ngo 1.18\n")
	diagnosticWriteFixture(t, root, "main.go", "package fixture\nfunc unused() { var x int; x = x; _ = x }\n")
	got := diagnosticExecuteFixture(t, NewCodeDiagnostics(root), `{"checker":"go_vet","paths":["main.go"],"timeout_ms":60000}`)
	if got.Status == "unverified" && len(got.Checks) == 1 && strings.Contains(got.Checks[0].Stderr, "bwrap:") {
		t.Skipf("native sandbox unavailable: %s", got.Checks[0].Stderr)
	}
	if got.Status != "fail" || !got.Complete || len(got.Diagnostics) == 0 || got.Diagnostics[0].File != "main.go" || got.Diagnostics[0].Check != "go_vet/assign" {
		t.Fatalf("go vet finding was not parsed: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("read-only check modified workspace: %v", err)
	}
}

func TestCodeDiagnosticsTypeScriptInstalledRuntimeIntegration(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("read-only native sandbox not installed")
	}
	// Reuse the repository's already installed compiler in a disposable fixture;
	// do not check the repository's application or install any dependencies.
	installed := filepath.Join("..", "..", "panel", "node_modules", "typescript")
	if _, err := os.Stat(filepath.Join(installed, "bin", "tsc")); err != nil {
		t.Skip("repository has no installed TypeScript compiler")
	}
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "node_modules", "typescript"), os.DirFS(installed)); err != nil {
		t.Fatal(err)
	}
	diagnosticWriteFixture(t, root, "broken.ts", "const value: number = 'wrong';\n")
	diagnosticWriteFixture(t, root, "clean.ts", "const value: number = 42;\n")
	got := diagnosticExecuteFixture(t, NewCodeDiagnostics(root), `{"checker":"typescript","paths":["broken.ts"],"timeout_ms":20000}`)
	if got.Status == "unverified" && len(got.Checks) > 0 && strings.Contains(got.Checks[0].Stderr, "bwrap:") {
		t.Skipf("native sandbox unavailable: %s", got.Checks[0].Stderr)
	}
	if got.Status != "fail" || !got.Complete || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "TS2322" || got.Diagnostics[0].File != "broken.ts" {
		t.Fatalf("TypeScript error was not parsed: %+v", got)
	}
	clean := diagnosticExecuteFixture(t, NewCodeDiagnostics(root), `{"checker":"typescript","paths":["clean.ts"],"timeout_ms":20000}`)
	if clean.Status != "pass" || !clean.Complete {
		t.Fatalf("valid TypeScript did not pass: %+v", clean)
	}
	for _, artifact := range []string{"broken.js", "clean.js", "tsconfig.tsbuildinfo"} {
		if _, err := os.Stat(filepath.Join(root, artifact)); !os.IsNotExist(err) {
			t.Fatalf("noEmit diagnostics produced %s: %v", artifact, err)
		}
	}
}
