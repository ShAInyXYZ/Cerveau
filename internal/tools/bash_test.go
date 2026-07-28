package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func bashArgs(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func TestBashBasicOutput(t *testing.T) {
	b := NewBash(t.TempDir())
	out, err := b.Execute(context.Background(), bashArgs("echo hello"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out == "" || out[:5] != "hello" {
		t.Fatalf("got %q", out)
	}
}

func TestBashKeepsOutputOnFailure(t *testing.T) {
	b := NewBash(t.TempDir())
	out, err := b.Execute(context.Background(), bashArgs("echo boom >&2; exit 3"))
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	// the command's own stderr must survive alongside the exit error
	if want := "boom"; !contains(out, want) {
		t.Fatalf("output %q missing %q", out, want)
	}
}

// The core regression: a command that backgrounds a long-lived child must NOT
// hang the tool. Cancelling the context has to kill the whole process group and
// return promptly — before this fix the leaked child held the output pipe open
// and Wait() blocked forever (a dev server ran 300s+ past its timeout).
func TestBashCancelKillsBackgroundedChild(t *testing.T) {
	b := NewBash(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()

	start := time.Now()
	// background a 60s sleep, then the shell "returns" but the child holds the pipe
	_, err := b.Execute(ctx, bashArgs("sleep 60 & echo started; wait"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected context-cancel error")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Execute hung for %s — process group not killed", elapsed)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
