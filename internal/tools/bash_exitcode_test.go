package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func runBash(t *testing.T, dir, command string) (string, error) {
	t.Helper()
	b := NewBash(dir)
	args, _ := json.Marshal(map[string]string{"command": command})
	return b.Execute(context.Background(), args)
}

// A test runner that RUNS correctly and reports failures exits non-zero. That
// is the tool answering the question, not the tool breaking.
//
// Treating it as a tool error killed a real benchmark: the model built a DAW,
// wrote a Playwright check, ran it three times while fixing the bugs it found,
// and the turn was stopped on guard_error_threshold — because each successful
// verification run counted as a failure. The model was doing exactly the right
// thing and the harness punished it for it.
func TestFailingTestIsNotAToolError(t *testing.T) {
	out, err := runBash(t, t.TempDir(), `echo "SOME CHECKS FAILED"; exit 1`)

	if err != nil {
		t.Errorf("non-zero exit reported as a tool error: %v", err)
	}
	if !strings.Contains(out, "SOME CHECKS FAILED") {
		t.Errorf("output lost: %q", out)
	}
	if !strings.Contains(out, "exit status 1") && !strings.Contains(out, "exit code 1") {
		t.Errorf("model can no longer see that the command exited non-zero: %q", out)
	}
}

// grep exits 1 for "no match" — an answer, not a malfunction.
func TestGrepNoMatchIsNotAToolError(t *testing.T) {
	if _, err := runBash(t, t.TempDir(), `echo hello | grep zzz`); err != nil {
		t.Errorf("grep no-match reported as a tool error: %v", err)
	}
}

// A command that could not run at all IS a real error the guard should count.
func TestMissingCommandStillErrors(t *testing.T) {
	if _, err := runBash(t, t.TempDir(), `definitely-not-a-real-binary-xyz`); err == nil {
		t.Error("a command that does not exist should still be a tool error")
	}
}
