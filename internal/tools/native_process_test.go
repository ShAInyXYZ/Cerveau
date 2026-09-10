package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireNativeSandbox(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed; sandbox execution unverified")
	}
}

func TestNativeProcessIsolationAndOutput(t *testing.T) {
	requireNativeSandbox(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "kept")
	if err := os.WriteFile(file, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	r := runNativeProcess(context.Background(), dir, "/bin/sh", []string{"-c", "echo stdout; echo stderr >&2; echo change > kept"}, nil, time.Second, true)
	if r.Err == nil || r.ExitCode == 0 || !strings.Contains(r.Stdout, "stdout") || !strings.Contains(r.Stderr, "stderr") {
		t.Fatalf("unexpected result %+v", r)
	}
	data, _ := os.ReadFile(file)
	if string(data) != "preserved" {
		t.Fatal("readonly file changed")
	}
}

func TestNativeProcessCancellationAndCaps(t *testing.T) {
	requireNativeSandbox(t)
	r := runNativeProcess(context.Background(), t.TempDir(), "/bin/sh", []string{"-c", "sleep 30 & wait"}, nil, 120*time.Millisecond, true)
	if !r.TimedOut || r.Err == nil || r.Elapsed > 2*time.Second {
		t.Fatalf("unbounded timeout %+v", r)
	}
	r = runNativeProcess(context.Background(), t.TempDir(), "/bin/sh", []string{"-c", "head -c 300000 /dev/zero"}, nil, time.Second, true)
	if r.Err != nil || !r.StdoutTruncated || len(r.Stdout) > browserProcessOutputLimit {
		t.Fatalf("unbounded output %+v", r)
	}
}

func TestNativeProcessSanitizesRuntimeOverrides(t *testing.T) {
	env := nativeProcessEnvironment([]string{"NODE_OPTIONS=--require=/bad", "NODE_PATH=/bad", "GOFLAGS=-race", "PATH=/usr/bin", "GOPATH=/existing"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "/bad") || strings.Contains(joined, "GOFLAGS=-race") || !strings.Contains(joined, "GOPATH=/existing") || !strings.Contains(joined, "GOPROXY=off") {
		t.Fatal(joined)
	}
}
