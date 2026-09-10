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

func runChecksFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"tests.mjs": "import assert from 'node:assert/strict'; assert.equal(2, 2);", "source.js": "export const n = 2;"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runChecksResult(t *testing.T, tool *RunChecks, args string) runChecksReceipt {
	t.Helper()
	out, err := tool.Execute(context.Background(), json.RawMessage(args))
	var r runChecksReceipt
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("receipt: %v, %s", err, out)
	}
	if (r.Status == "pass") != (err == nil) {
		t.Fatalf("tool error must agree with status %s: %v", r.Status, err)
	}
	return r
}

func TestRunChecksBoundedSummaryRetainsFullHashedReceipt(t *testing.T) {
	dir := runChecksFixture(t)
	tool := NewRunChecks(dir)
	tool.lookup = func(string) (string, error) { return "/usr/bin/node", nil }
	fullOutput := strings.Repeat("actual assertion diagnostics\n", 4000)
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		return nativeProcessResult{Stdout: fullOutput, ExitCode: 0}
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"runner":"node_script","paths":["tests.mjs"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 7500 {
		t.Fatalf("model-facing summary is %d bytes, exceeds 7500", len(out))
	}
	var summary struct {
		Status           string `json:"status"`
		SummaryTruncated bool   `json:"summary_truncated"`
		Evidence         struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Bytes  int    `json:"bytes"`
		} `json:"evidence"`
		Omitted map[string]int `json:"omitted"`
	}
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Status != "pass" || !summary.SummaryTruncated || summary.Omitted["stdout_bytes"] == 0 {
		t.Fatalf("omissions not explicit: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, summary.Evidence.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != summary.Evidence.Bytes || runChecksSHA(data) != summary.Evidence.SHA256 {
		t.Fatal("receipt integrity metadata differs from retained bytes")
	}
	var receipt runChecksReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Stdout != fullOutput {
		t.Fatal("bounded model summary discarded full receipt evidence")
	}
}

func TestRunChecksNonpassReturnsToolError(t *testing.T) {
	for _, code := range []int{1, -1} {
		tool := NewRunChecks(runChecksFixture(t))
		tool.lookup = func(string) (string, error) { return "/usr/bin/node", nil }
		tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
			return nativeProcessResult{ExitCode: code, Err: errors.New("runner exit")}
		}
		out, err := tool.Execute(context.Background(), json.RawMessage(`{"runner":"node_script","paths":["tests.mjs"]}`))
		if err == nil || !json.Valid([]byte(out)) {
			t.Fatalf("nonpass must return JSON evidence and tool error: %v %s", err, out)
		}
	}
}

func TestRunChecksSummaryBoundIncludesJSONEscapingAndInventories(t *testing.T) {
	r := &runChecksReceipt{Version: 1, Runner: "node_test", Status: "fail", ReceiptPath: ".devcheck/run-checks/test.json", Identity: strings.Repeat("a", 64), InputSHA256: strings.Repeat("b", 64), Stdout: strings.Repeat("\x01", 10000), Stderr: strings.Repeat("\x02", 10000)}
	for i := 0; i < 100; i++ {
		r.Tests = append(r.Tests, runChecksTest{Name: strings.Repeat("\x03", 150), Status: "fail", Package: strings.Repeat("\x04", 150), Location: strings.Repeat("\x05", 150), Evidence: strings.Repeat("\x06", 500)})
		r.Before = append(r.Before, runChecksVersion{Path: strings.Repeat("path", 100), SHA256: strings.Repeat("c", 64)})
	}
	r.After = r.Before
	r.Command.Args = []string{strings.Repeat("x", 10000)}
	full, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	out, err := summarizeRunChecks(r, full)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 7500 || !json.Valid([]byte(out)) {
		t.Fatalf("escaped summary exceeds complete-JSON bound: %d bytes", len(out))
	}
	if len(r.Tests) != 100 || len(r.Before) != 100 || len(r.Stdout) != 10000 {
		t.Fatal("compaction mutated complete receipt")
	}
}

func TestRunChecksRejectsUnboundedAndEscapingInputs(t *testing.T) {
	dir := runChecksFixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "test.mjs"), []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	tool := NewRunChecks(dir)
	for _, input := range []string{
		`{"runner":"bash","paths":["tests.mjs"]}`,
		`{"runner":"node_script","paths":["../test.mjs"]}`,
		`{"runner":"node_script","paths":["escape/test.mjs"]}`,
		`{"runner":"node_script","paths":["/tmp/test.mjs"]}`,
		`{"runner":"node_script","paths":["tests.mjs"],"flags":["--eval"]}`,
		`{"runner":"node_script","paths":["tests.mjs"],"timeout_seconds":121}`,
		`{"runner":"node_script","paths":["tests.mjs"],"timeout_seconds":0}`,
		`{"runner":"node_script","paths":["tests.mjs"],"prior_receipt":"source.js"}`,
		`{"runner":"node_script","paths":["tests.mjs"]} {}`,
	} {
		if out, err := tool.Execute(context.Background(), json.RawMessage(input)); err == nil {
			t.Errorf("accepted %s: %s", input, out)
		}
	}
}

func TestRunChecksComparisonReportsTransitionNotOutputProgress(t *testing.T) {
	tool := NewRunChecks(runChecksFixture(t))
	tool.lookup = func(string) (string, error) { return "/usr/bin/node", nil }
	output := "failure one"
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		return nativeProcessResult{ExitCode: 1, Stdout: output, Err: errors.New("exit status 1")}
	}
	first := runChecksResult(t, tool, `{"runner":"node_script","paths":["tests.mjs"]}`)
	output = "entirely different failure output"
	args, _ := json.Marshal(map[string]any{"runner": "node_script", "paths": []string{"tests.mjs"}, "prior_receipt": first.ReceiptPath})
	second := runChecksResult(t, tool, string(args))
	if second.Status != "fail" || second.Comparison == nil || !second.Comparison.Comparable || second.Comparison.Transition != "fail_to_fail" {
		t.Fatalf("output was confused with progress: %+v", second)
	}
	args, _ = json.Marshal(map[string]any{"runner": "node_test", "paths": []string{"tests.mjs"}, "prior_receipt": first.ReceiptPath})
	other := runChecksResult(t, tool, string(args))
	if other.Comparison == nil || other.Comparison.Comparable {
		t.Fatalf("different runner compared: %+v", other)
	}
}

func TestRunChecksRefusesEvidenceSymlinkEscape(t *testing.T) {
	dir := runChecksFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".devcheck")); err != nil {
		t.Fatal(err)
	}
	tool := NewRunChecks(dir)
	tool.lookup = func(string) (string, error) { return "", exec.ErrNotFound }
	if out, err := tool.Execute(context.Background(), json.RawMessage(`{"runner":"node_script","paths":["tests.mjs"]}`)); err == nil {
		t.Fatalf("evidence escape accepted: %s", out)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("wrote outside workspace")
	}
}

func TestRunChecksRealNodeRunners(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("read-only sandbox unavailable")
	}
	dir := runChecksFixture(t)
	tool := NewRunChecks(dir)
	pass := runChecksResult(t, tool, `{"runner":"node_script","paths":["tests.mjs"]}`)
	if pass.Status == "unverified" && strings.Contains(pass.Stderr, "bwrap:") {
		t.Skipf("sandbox unavailable: %s", pass.Stderr)
	}
	if pass.Status != "pass" {
		t.Fatalf("real Node script: %+v", pass)
	}
	failure := "import test from 'node:test'; import assert from 'node:assert/strict'; test('actual inventory count', () => assert.equal(3, 2));\n"
	if err := os.WriteFile(filepath.Join(dir, "node.test.mjs"), []byte(failure), 0600); err != nil {
		t.Fatal(err)
	}
	fail := runChecksResult(t, tool, `{"runner":"node_test","paths":["node.test.mjs"]}`)
	if fail.Status != "fail" || len(fail.Tests) != 1 || fail.Tests[0].Name != "actual inventory count" || string(fail.Tests[0].Expected) != "2" || string(fail.Tests[0].Actual) != "3" || fail.Tests[0].Location == "" {
		t.Fatalf("real Node TAP: %+v", fail)
	}
	readonly := "import fs from 'node:fs'; import assert from 'node:assert/strict'; assert.throws(() => fs.writeFileSync('source.js', 'changed'));\n"
	if err := os.WriteFile(filepath.Join(dir, "readonly.mjs"), []byte(readonly), 0600); err != nil {
		t.Fatal(err)
	}
	readOnly := runChecksResult(t, tool, `{"runner":"node_script","paths":["readonly.mjs"]}`)
	if readOnly.Status != "pass" {
		t.Fatalf("workspace was writable: %+v", readOnly)
	}
}

func TestRunChecksRealGoRunner(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go unavailable")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("read-only sandbox unavailable")
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module example.test/checkfixture\n\ngo 1.25.0\n", "count_test.go": "package checkfixture\nimport \"testing\"\nfunc TestCount(t *testing.T) { if 2+2 != 4 { t.Fatal(\"arithmetic\") } }\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := runChecksResult(t, NewRunChecks(dir), `{"runner":"go_test","paths":["."],"timeout_seconds":60}`)
	if r.Status == "unverified" && strings.Contains(r.Stderr, "bwrap:") {
		t.Skipf("sandbox unavailable: %s", r.Stderr)
	}
	if r.Status != "pass" || len(r.Tests) != 1 || r.Tests[0].Name != "TestCount" {
		t.Fatalf("real Go JSON: %+v", r)
	}
}

func TestRunChecksNodeScriptCommandAndEvidence(t *testing.T) {
	dir := runChecksFixture(t)
	tool := NewRunChecks(dir)
	tool.lookup = func(string) (string, error) { return "/usr/bin/node", nil }
	tool.run = func(_ context.Context, ws, executable string, args []string, stdin []byte, timeout time.Duration, readOnly bool) nativeProcessResult {
		if ws != dir || executable != "/usr/bin/node" || !readOnly || stdin != nil || timeout != 30*time.Second {
			t.Errorf("unexpected execution context")
		}
		if strings.Join(args, " ") != "--experimental-default-type=module ./tests.mjs" {
			t.Errorf("argv = %q", args)
		}
		return nativeProcessResult{Stdout: "all checks passed\n", ExitCode: 0, Elapsed: 3 * time.Millisecond}
	}
	r := runChecksResult(t, tool, `{"runner":"node_script","paths":["tests.mjs"],"source_paths":["source.js"]}`)
	if r.Status != "pass" || len(r.Tests) != 0 || r.ElapsedMS != 3 || r.Command.Executable != "/usr/bin/node" {
		t.Fatalf("bad receipt: %+v", r)
	}
	if r.Stdout != "all checks passed\n" || len(r.Before) != 2 || len(r.After) != 2 || r.Before[0].SHA256 == "" {
		t.Fatalf("missing evidence: %+v", r)
	}
	data, err := os.ReadFile(filepath.Join(dir, r.ReceiptPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "all checks passed") {
		t.Fatal("receipt discarded output")
	}
}

func TestRunChecksMissingRuntimeTimeoutAndTruncationAreUnverified(t *testing.T) {
	for _, mode := range []string{"missing", "timeout", "truncated", "sandbox"} {
		t.Run(mode, func(t *testing.T) {
			tool := NewRunChecks(runChecksFixture(t))
			tool.lookup = func(string) (string, error) {
				if mode == "missing" {
					return "", exec.ErrNotFound
				}
				return "/usr/bin/node", nil
			}
			tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
				switch mode {
				case "timeout":
					return nativeProcessResult{ExitCode: -1, TimedOut: true, Err: context.DeadlineExceeded}
				case "truncated":
					return nativeProcessResult{ExitCode: 0, StdoutTruncated: true}
				default:
					return nativeProcessResult{ExitCode: -1, Err: errors.New("sandbox unavailable")}
				}
			}
			r := runChecksResult(t, tool, `{"runner":"node_script","paths":["tests.mjs"]}`)
			if r.Status != "unverified" {
				t.Fatalf("%s status = %s", mode, r.Status)
			}
		})
	}
}

func TestRunChecksChangedProtectedCheckInvalidatesPassAndPriorComparison(t *testing.T) {
	dir := runChecksFixture(t)
	tool := NewRunChecks(dir)
	tool.lookup = func(string) (string, error) { return "/usr/bin/node", nil }
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		return nativeProcessResult{ExitCode: 0}
	}
	first := runChecksResult(t, tool, `{"runner":"node_script","paths":["tests.mjs"]}`)
	if err := os.WriteFile(filepath.Join(dir, "tests.mjs"), []byte("// assertions removed"), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"runner": "node_script", "paths": []string{"tests.mjs"}, "prior_receipt": first.ReceiptPath})
	second := runChecksResult(t, tool, string(args))
	if second.Status != "unverified" || second.Comparison == nil || second.Comparison.Comparable {
		t.Fatalf("changed checks accepted: %+v", second)
	}
	tool.run = func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
		if err := os.WriteFile(filepath.Join(dir, "tests.mjs"), []byte("// changed during execution"), 0600); err != nil {
			t.Fatal(err)
		}
		return nativeProcessResult{ExitCode: 0}
	}
	during := runChecksResult(t, tool, `{"runner":"node_script","paths":["tests.mjs"]}`)
	if during.Status != "unverified" {
		t.Fatalf("changed during run accepted: %+v", during)
	}
}

func TestRunChecksParsesTAPFailuresAndDoesNotInventCounts(t *testing.T) {
	tap := "TAP version 13\n# Subtest: inventory rejects duplicates\nnot ok 1 - inventory rejects duplicates\n  ---\n  duration_ms: 1.2\n  location: '/workspace/test.mjs:4:1'\n  failureType: 'testCodeFailure'\n  error: 'different counts'\n  code: 'ERR_ASSERTION'\n  expected: 2\n  actual: 3\n  operator: 'strictEqual'\n  ...\n1..1\n# tests 1\n# pass 0\n# fail 1\n"
	tests, complete := parseRunChecksTAP(tap)
	if !complete || len(tests) != 1 || tests[0].Name != "inventory rejects duplicates" || tests[0].Status != "fail" || tests[0].Location != "/workspace/test.mjs:4:1" || string(tests[0].Expected) != "2" || string(tests[0].Actual) != "3" {
		t.Fatalf("TAP parse: %+v complete=%v", tests, complete)
	}
	for _, bogus := range []string{"PASS: 900 checks", "TAP version 13\nok 1 - partial\n", "TAP version 13\n1..0\n"} {
		if _, complete := parseRunChecksTAP(bogus); complete {
			t.Errorf("claimed complete evidence for %q", bogus)
		}
	}
}

func TestRunChecksParsesGoJSON(t *testing.T) {
	output := "{\"Action\":\"run\",\"Package\":\"example.test/demo\",\"Test\":\"TestCount\"}\n{\"Action\":\"output\",\"Package\":\"example.test/demo\",\"Test\":\"TestCount\",\"Output\":\"    count_test.go:7: expected 2, got 3\\n\"}\n{\"Action\":\"fail\",\"Package\":\"example.test/demo\",\"Test\":\"TestCount\",\"Elapsed\":0.01}\n{\"Action\":\"fail\",\"Package\":\"example.test/demo\",\"Elapsed\":0.02}\n"
	tests, complete := parseRunChecksGo(output)
	if !complete || len(tests) != 1 || tests[0].Name != "TestCount" || tests[0].Status != "fail" || tests[0].Location != "count_test.go:7" || len(tests[0].Expected) != 0 || len(tests[0].Actual) != 0 {
		t.Fatalf("Go parse: %+v complete=%v", tests, complete)
	}
	if _, complete := parseRunChecksGo("{\"Action\":\"run\",\"Test\":\"TestIncomplete\"}\n"); complete {
		t.Fatal("partial stream accepted")
	}
}
