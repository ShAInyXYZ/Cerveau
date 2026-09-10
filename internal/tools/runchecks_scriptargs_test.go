package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunChecksScriptArgsStayAfterScriptAndIdentifyCheck(t *testing.T) {
	tool := NewRunChecks(runChecksFixture(t))
	tool.lookup = func(string) (string, error) { return "/usr/bin/node", nil }
	scriptArgs := []string{"skylight", "has space", "--eval", "process.exit(0)", "$(touch injected); echo injected > injected"}
	want := append([]string{"--experimental-default-type=module", "./tests.mjs"}, scriptArgs...)
	tool.run = func(_ context.Context, _ string, _ string, argv []string, stdin []byte, _ time.Duration, readOnly bool) nativeProcessResult {
		if !reflect.DeepEqual(argv, want) || stdin != nil || !readOnly {
			t.Fatalf("script args changed command or sandbox: argv=%q stdin=%q readonly=%t", argv, stdin, readOnly)
		}
		return nativeProcessResult{ExitCode: 0}
	}
	args := map[string]any{"runner": "node_script", "paths": []string{"tests.mjs"}, "script_args": scriptArgs}
	raw, _ := json.Marshal(args)
	first := runChecksResult(t, tool, string(raw))
	if !reflect.DeepEqual(first.Command.Args, want) || first.VerificationScope != "script_exit_status_only_assertion_count_unknown" {
		t.Fatalf("receipt lost script argument identity or scope: %+v", first)
	}
	args["prior_receipt"] = first.ReceiptPath
	raw, _ = json.Marshal(args)
	same := runChecksResult(t, tool, string(raw))
	if same.Identity != first.Identity || same.Comparison == nil || !same.Comparison.Comparable {
		t.Fatalf("same script arguments must compare: %+v", same.Comparison)
	}
	scriptArgs[0] = "blocklight"
	want[2] = "blocklight"
	raw, _ = json.Marshal(args)
	changed := runChecksResult(t, tool, string(raw))
	if changed.Identity == first.Identity || changed.InputSHA256 == first.InputSHA256 || changed.Comparison == nil || changed.Comparison.Comparable {
		t.Fatalf("different script groups were compared: %+v", changed.Comparison)
	}
}

func TestRunChecksRejectsInvalidScriptArgsBeforeExecution(t *testing.T) {
	tool := NewRunChecks(runChecksFixture(t))
	tool.lookup = func(string) (string, error) {
		t.Fatal("invalid script_args reached runtime lookup")
		return "", nil
	}
	tooMany := make([]string, runChecksMaxScriptArgs+1)
	for i := range tooMany {
		tooMany[i] = "group"
	}
	for _, test := range []struct {
		name, runner string
		args         any
	}{
		{"wrong_runner_node_test", "node_test", []string{"skylight"}},
		{"wrong_runner_go_test", "go_test", []string{"skylight"}},
		{"wrong_runner_empty_array", "node_test", []string{}},
		{"null_array", "node_script", nil},
		{"null_entry", "node_script", []any{nil}},
		{"not_array", "node_script", "skylight"},
		{"nonstring", "node_script", []any{1}},
		{"empty", "node_script", []string{""}},
		{"nul", "node_script", []string{"sky\x00light"}},
		{"too_many", "node_script", tooMany},
		{"too_long", "node_script", []string{strings.Repeat("x", runChecksMaxScriptArgBytes+1)}},
		{"utf8_bytes_not_runes", "node_script", []string{strings.Repeat("é", runChecksMaxScriptArgBytes/2+1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"runner": test.runner, "paths": []string{"tests.mjs"}, "script_args": test.args})
			if out, err := tool.Execute(context.Background(), raw); err == nil {
				t.Fatalf("accepted invalid script_args: %s", out)
			}
		})
	}
	invalidUTF8 := []byte("{\"runner\":\"node_script\",\"paths\":[\"tests.mjs\"],\"script_args\":[\"\xff\"]}")
	if out, err := tool.Execute(context.Background(), invalidUTF8); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid UTF-8 was silently replaced: output=%s err=%v", out, err)
	}
}

func TestRunChecksScriptArgsBoundsAccepted(t *testing.T) {
	tool := NewRunChecks(runChecksFixture(t))
	tool.lookup = func(string) (string, error) { return "/usr/bin/node", nil }
	scriptArgs := make([]string, runChecksMaxScriptArgs)
	for i := range scriptArgs {
		scriptArgs[i] = strings.Repeat("é", runChecksMaxScriptArgBytes/2)
	}
	tool.run = func(_ context.Context, _ string, _ string, argv []string, _ []byte, _ time.Duration, _ bool) nativeProcessResult {
		if !reflect.DeepEqual(argv[2:], scriptArgs) {
			t.Fatal("valid boundary arguments changed")
		}
		return nativeProcessResult{ExitCode: 0}
	}
	raw, _ := json.Marshal(map[string]any{"runner": "node_script", "paths": []string{"tests.mjs"}, "script_args": scriptArgs})
	if r := runChecksResult(t, tool, string(raw)); r.Status != "pass" {
		t.Fatalf("valid boundary arguments rejected: %+v", r)
	}
}

func TestRunChecksRealNodeScriptArgsAreLiteralAndKeepFailure(t *testing.T) {
	for _, name := range []string{"node", "bwrap"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s unavailable", name)
		}
	}
	dir := runChecksFixture(t)
	literals := []string{"has space", "--eval", "process.exit(0)", "$(touch /tmp/cerveau-script-args-injected); echo injected"}
	expected, _ := json.Marshal(literals)
	source := "import assert from 'node:assert/strict';\n" +
		"assert.deepEqual(process.argv.slice(3), " + string(expected) + ");\n" +
		"assert.equal(process.argv[2], 'skylight', 'selected test group');\n" +
		"console.log('selected skylight checks passed');\n"
	if err := os.WriteFile(filepath.Join(dir, "tests.mjs"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"runner": "node_script", "paths": []string{"tests.mjs"}, "script_args": append([]string{"skylight"}, literals...)}
	raw, _ := json.Marshal(args)
	pass := runChecksResult(t, NewRunChecks(dir), string(raw))
	if pass.Status == "unverified" && strings.Contains(pass.Stderr, "bwrap:") {
		t.Skipf("sandbox unavailable: %s", pass.Stderr)
	}
	if pass.Status != "pass" || !strings.Contains(pass.Stdout, "selected skylight checks passed") {
		t.Fatalf("literal script arguments did not reach Node: %+v", pass)
	}
	args["script_args"] = append([]string{"blocklight"}, literals...)
	raw, _ = json.Marshal(args)
	fail := runChecksResult(t, NewRunChecks(dir), string(raw))
	if fail.Status != "fail" || fail.ExitCode != 1 || !strings.Contains(fail.Stderr, "selected test group") {
		t.Fatalf("failed selected assertion returned success: %+v", fail)
	}
}
