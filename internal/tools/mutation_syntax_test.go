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

func requireMutationSyntaxRuntime(t *testing.T) {
	t.Helper()
	for _, name := range []string{"node", "bwrap"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s unavailable; supported JS mutations fail closed", name)
		}
	}
}

func mutationTestArgs(tool, path, before, after string) json.RawMessage {
	args := map[string]any{"path": path, "old_string": before, "new_string": after}
	if tool == "write" {
		args = map[string]any{"path": path, "content": after}
	} else if tool == "apply_patch" {
		args = map[string]any{"edits": []map[string]any{args}}
	}
	raw, _ := json.Marshal(args)
	return raw
}

func TestMutationSyntaxRejectsIncompleteFunctionWithoutWrites(t *testing.T) {
	requireMutationSyntaxRuntime(t)
	before := "function flushUpdates() {\n  return 1;\n}\n"
	after := before + "function syncLight(){\n"
	for _, tool := range []string{"edit", "write", "apply_patch"} {
		t.Run(tool, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "world.js")
			if err := os.WriteFile(path, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := setupPatchReg(t, dir).Execute(context.Background(), tool, mutationTestArgs(tool, "world.js", before, after))
			if err == nil || !strings.Contains(err.Error(), "syntax regression") || !strings.Contains(err.Error(), "world.js:5") || !strings.Contains(err.Error(), "no edits applied") {
				t.Fatalf("missing actionable syntax rejection: %s %v", out, err)
			}
			if !strings.Contains(out, `"status":"rejected"`) || !strings.Contains(out, sourceSHA256(before)) || !strings.Contains(out, sourceSHA256(after)) {
				t.Fatalf("rejected images lack versioned evidence: %s", out)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != before {
				t.Fatalf("syntax rejection changed original bytes: %q %v", got, err)
			}
		})
	}
}

func TestMutationSyntaxRejectsInvalidNewFileBeforeCreatingDirectories(t *testing.T) {
	requireMutationSyntaxRuntime(t)
	for _, tool := range []string{"write", "apply_patch"} {
		t.Run(tool, func(t *testing.T) {
			dir := t.TempDir()
			_, err := setupPatchReg(t, dir).Execute(context.Background(), tool, mutationTestArgs(tool, "new/helper.mjs", "", "function helper(){"))
			if err == nil || !strings.Contains(err.Error(), "syntax regression") {
				t.Fatalf("invalid new file accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "new")); !os.IsNotExist(err) {
				t.Fatal("syntax validation created target directories")
			}
		})
	}
}

func TestMutationSyntaxPatchValidatesFinalBatchOnly(t *testing.T) {
	requireMutationSyntaxRuntime(t)
	dir := t.TempDir()
	before := "function first() { return 1; }\nfunction second() { return 2; }\n"
	intermediate := "function first() { return 3;\nfunction second() { return 2; }\n"
	after := "function first() { return 3;\n}\nfunction second() { return 2; }\n"
	path := filepath.Join(dir, "world.js")
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"edits": []map[string]string{
		{"path": "world.js", "old_string": "return 1; }", "new_string": "return 3;", "expected_sha256": sourceSHA256(before)},
		{"path": "./world.js", "old_string": "function second()", "new_string": "}\nfunction second()", "expected_sha256": sourceSHA256(intermediate)},
	}})
	out, err := setupPatchReg(t, dir).Execute(context.Background(), "apply_patch", raw)
	if err != nil || !strings.Contains(out, "2/2 hunks applied") || !strings.Contains(out, `"status":"pass"`) {
		t.Fatalf("valid final batch rejected an intermediate imbalance: %s %v", out, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != after {
		t.Fatalf("final bytes differ: %q", got)
	}
}

func TestMutationSyntaxPatchRejectsBeforeUnrelatedFileChanges(t *testing.T) {
	requireMutationSyntaxRuntime(t)
	dir := t.TempDir()
	for name, body := range map[string]string{"note.txt": "original", "world.js": "function complete() {}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw := json.RawMessage(`{"edits":[{"path":"note.txt","old_string":"original","new_string":"changed"},{"path":"world.js","old_string":"{}","new_string":"{"}]}`)
	if _, err := setupPatchReg(t, dir).Execute(context.Background(), "apply_patch", raw); err == nil {
		t.Fatal("invalid final batch accepted")
	}
	for name, want := range map[string]string{"note.txt": "original", "world.js": "function complete() {}\n"} {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if string(got) != want {
			t.Fatalf("%s changed before final syntax validation: %q", name, got)
		}
	}
}

func TestMutationSyntaxBrokenSourceRemainsRepairable(t *testing.T) {
	requireMutationSyntaxRuntime(t)
	for _, after := range []string{"function repaired() {", "function repaired() {}\n"} {
		dir := t.TempDir()
		before := "function broken() {"
		if err := os.WriteFile(filepath.Join(dir, "world.js"), []byte(before), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := NewEdit(dir).Execute(context.Background(), mutationTestArgs("edit", "world.js", before, after))
		if err != nil {
			t.Fatalf("preexisting broken source cannot be repaired: %s %v", out, err)
		}
		status := "pass"
		if after == "function repaired() {" {
			status = "baseline_invalid"
		}
		if !strings.Contains(out, `"status":"`+status+`"`) {
			t.Fatalf("repair syntax status not explicit: %s", out)
		}
	}
}

func TestMutationSyntaxDoesNotExecuteProjectSource(t *testing.T) {
	requireMutationSyntaxRuntime(t)
	dir := t.TempDir()
	after := "import fs from 'node:fs'; import './does-not-exist.js'; fs.writeFileSync('executed', 'bad'); throw new Error('must not execute');\n"
	out, err := NewWrite(dir).Execute(context.Background(), mutationTestArgs("write", "world.mjs", "", after))
	if err != nil || !strings.Contains(out, `"status":"pass"`) {
		t.Fatalf("syntax-only parse resolved imports or ran source: %s %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "executed")); !os.IsNotExist(err) {
		t.Fatal("project code executed during syntax validation")
	}
}

func TestMutationSyntaxPatchProofRejectsRemediationChanges(t *testing.T) {
	requireMutationSyntaxRuntime(t)
	dir := t.TempDir()
	before := "function complete() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "world.js"), []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	reg := setupPatchReg(t, dir)
	reg.SetRemediator(func(name string, raw json.RawMessage) (json.RawMessage, error) {
		if name != "edit" {
			return raw, nil
		}
		var args map[string]any
		_ = json.Unmarshal(raw, &args)
		args["new_string"] = "function incomplete() {"
		return json.Marshal(args)
	})
	out, err := reg.Execute(context.Background(), "apply_patch", mutationTestArgs("apply_patch", "world.js", before, "function updated() {}\n"))
	if err == nil || !strings.Contains(err.Error(), "changed after syntax validation") {
		t.Fatalf("changed intermediate image reused batch authorization: %s %v", out, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "world.js"))
	if string(got) != before {
		t.Fatal("remediated hunk bypassed syntax authorization")
	}
}

func TestMutationSyntaxStaleSHAAndUnsupportedTypes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	before := "const state = 1;\n"
	if err := os.WriteFile(filepath.Join(dir, "world.js"), []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"path": "world.js", "old_string": before, "new_string": "function incomplete(){", "expected_sha256": sourceSHA256("stale")})
	if _, err := NewEdit(dir).Execute(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "stale source") {
		t.Fatalf("stale source reached parser: %v", err)
	}
	out, err := NewWrite(dir).Execute(context.Background(), mutationTestArgs("write", "world.ts", "", "const state: number = 1;"))
	if err != nil || !strings.Contains(out, "syntax unverified") {
		t.Fatalf("unsupported language silently claimed syntax verification: %s %v", out, err)
	}
	_, err = NewWrite(dir).Execute(context.Background(), mutationTestArgs("write", "world.js", before, "const state = 2;"))
	if err == nil || !strings.Contains(err.Error(), "runtime unavailable") {
		t.Fatalf("missing parser did not fail closed: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "world.js"))
	if string(got) != before {
		t.Fatal("unavailable parser changed source")
	}
}

func TestMutationSyntaxUnavailableChecksNeverAuthorizeWrites(t *testing.T) {
	for _, mode := range []string{"sandbox", "timeout", "truncated", "unsupported_output"} {
		t.Run(mode, func(t *testing.T) {
			validator := mutationSyntaxValidator{lookup: func(string) (string, error) { return "/usr/bin/node", nil }}
			validator.run = func(_ context.Context, _, _ string, argv []string, stdin []byte, timeout time.Duration, readOnly bool) nativeProcessResult {
				if strings.Join(argv, " ") != "--check --input-type=module" || string(stdin) != "const state = 2;" || timeout > mutationSyntaxTimeout || !readOnly {
					t.Fatal("parser command or sandbox scope changed")
				}
				switch mode {
				case "sandbox":
					return nativeProcessResult{ExitCode: -1, Err: errors.New("sandbox unavailable")}
				case "timeout":
					return nativeProcessResult{ExitCode: -1, TimedOut: true}
				case "truncated":
					return nativeProcessResult{ExitCode: 0, StderrTruncated: true}
				default:
					return nativeProcessResult{ExitCode: 1, Stderr: "unsupported runtime version"}
				}
			}
			out, err := validator.validate(context.Background(), t.TempDir(), "world.js", "world.js", "const state = 1;", "const state = 2;")
			if err == nil || !strings.Contains(out, `"status":"unverified"`) || !strings.Contains(err.Error(), "no edits applied") {
				t.Fatalf("unavailable parse authorized mutation: %s %v", out, err)
			}
		})
	}
}

func TestMutationSyntaxModuleModesAndSymlinkAliases(t *testing.T) {
	requireMutationSyntaxRuntime(t)
	for _, extension := range []string{"js", "mjs", "cjs"} {
		t.Run(extension, func(t *testing.T) {
			dir := t.TempDir()
			out, err := NewWrite(dir).Execute(context.Background(), mutationTestArgs("write", "source."+extension, "", "return 1;\n"))
			if extension == "mjs" {
				if err == nil || !strings.Contains(out, `"status":"rejected"`) {
					t.Fatalf("ES module accepted top-level return: %s %v", out, err)
				}
			} else if err != nil || !strings.Contains(out, `"status":"pass"`) {
				t.Fatalf("CommonJS-compatible source rejected: %s %v", out, err)
			}
		})
	}
	dir := t.TempDir()
	before := "function complete() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "world.js"), []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("world.js", filepath.Join(dir, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	_, err := NewEdit(dir).Execute(context.Background(), mutationTestArgs("edit", "alias.txt", before, "function incomplete(){"))
	if err == nil || !strings.Contains(err.Error(), "syntax regression") {
		t.Fatalf("symlink extension bypassed syntax gate: %v", err)
	}
}

func TestMutationSyntaxRefusesWorkspaceParserAndCanceledValidation(t *testing.T) {
	dir := t.TempDir()
	parser := filepath.Join(dir, "node")
	if err := os.WriteFile(parser, []byte("project code, never execute"), 0700); err != nil {
		t.Fatal(err)
	}
	validator := mutationSyntaxValidator{
		lookup: func(string) (string, error) { return parser, nil },
		run: func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult {
			t.Fatal("workspace-supplied parser executed")
			return nativeProcessResult{}
		},
	}
	if _, err := validator.validate(context.Background(), dir, "world.js", "world.js", "", "const value = 1;"); err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("workspace code was trusted as installed parser: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewWrite(dir).Execute(ctx, mutationTestArgs("write", "world.js", "", "const value = 1;")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled validation allowed mutation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "world.js")); !os.IsNotExist(err) {
		t.Fatal("canceled validation created source")
	}
}
