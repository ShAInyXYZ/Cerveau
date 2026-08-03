package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// When output overflows the cap, the TAIL must be kept: a build/test failure
// puts its error at the end, and head-only truncation discards exactly the
// lines the model needs to fix the problem.
func TestBashCapKeepsTail(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&sb, "progress line %d..............................\n", i)
	}
	sb.WriteString("FATAL: the actual error is on the last line\n")
	capped := capOutput(sb.String())

	if !strings.Contains(capped, "FATAL: the actual error is on the last line") {
		t.Fatal("cap dropped the tail — the error line is gone")
	}
	if len(capped) > bashCapChars+200 {
		t.Fatalf("cap not enforced: %d bytes", len(capped))
	}
}

// A backgrounded server must be REFUSED before running — the hint-after-failure
// approach burned whole turns (the model ran it, waited out the kill, and the
// server died anyway). Structural rule: refuse up front, point at serve.
func TestBashRefusesBackgroundedServer(t *testing.T) {
	b := NewBash(t.TempDir())
	_, err := b.Execute(context.Background(), json.RawMessage(
		`{"command":"cd app && python3 -m http.server 8888 &"}`))
	if err == nil {
		t.Fatal("backgrounded server should be refused")
	}
	if !strings.Contains(err.Error(), "serve") {
		t.Errorf("refusal should point at the serve tool: %v", err)
	}
}

// && is not a background operator — plain build chains must still run.
func TestBashAllowsAndAndChains(t *testing.T) {
	b := NewBash(t.TempDir())
	out, err := b.Execute(context.Background(), json.RawMessage(
		`{"command":"echo one && echo two"}`))
	if err != nil {
		t.Fatalf("&& chain should run: %v", err)
	}
	if !strings.Contains(out, "two") {
		t.Errorf("chain output missing: %q", out)
	}
}
