package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type nativeProcessResult struct {
	Stdout, Stderr                   string
	Err                              error
	ExitCode                         int
	TimedOut                         bool
	Elapsed                          time.Duration
	StdoutTruncated, StderrTruncated bool
}

// Trusted host-only additions, not fields in any model-facing schema.
type nativeProcessReadPathsKey struct{}
type nativeProcessEnvironmentKey struct{}

// All child code has a read-only host, private /tmp, and a PID namespace so
// detached grandchildren cannot survive cancellation. Checks also get a
// read-only workspace and no network. Browser procedures write evidence only
// in their workspace and apply their own exact-origin network policy.
func runNativeProcess(ctx context.Context, workspace, executable string, args []string, stdin []byte, timeout time.Duration, readOnly bool) (r nativeProcessResult) {
	started := time.Now()
	r.ExitCode = -1
	defer func() { r.Elapsed = time.Since(started) }()
	if timeout <= 0 || timeout > 180*time.Second || len(stdin) > 4<<20 {
		r.Err = errors.New("invalid native process time/input bound")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		r.Err = err
		r.TimedOut = errors.Is(err, context.DeadlineExceeded)
		return
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		r.Err = err
		return
	}
	root, err = filepath.Abs(root)
	if err != nil {
		r.Err = err
		return
	}
	if root == "/" {
		r.Err = errors.New("native execution requires a scoped workspace, not filesystem root")
		return
	}
	program, err := exec.LookPath(executable)
	if err != nil {
		r.Err = err
		return
	}
	program, err = filepath.Abs(program)
	if err != nil {
		r.Err = err
		return
	}
	isolation, err := exec.LookPath("bwrap")
	if err != nil {
		r.Err = errors.New("native execution unavailable: bubblewrap required; no unprotected fallback")
		return
	}
	argv := []string{"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--unshare-pid", "--die-with-parent"}
	if readOnly {
		argv = append(argv, "--unshare-net", "--ro-bind", root, root)
	} else {
		argv = append(argv, "--bind", root, root)
	}
	if paths, ok := ctx.Value(nativeProcessReadPathsKey{}).([]string); ok {
		for _, path := range paths {
			if !filepath.IsAbs(path) {
				r.Err = errors.New("native runtime path must be absolute")
				return
			}
			argv = append(argv, "--ro-bind", path, path)
		}
	}
	argv = append(argv, "--dir", "/tmp/cerveau-check-cache", "--dir", "/tmp/cerveau-check-tmp", "--chdir", root, "--", program)
	argv = append(argv, args...)
	cmd := exec.Command(isolation, argv...)
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = nativeProcessEnvironment(os.Environ())
	if extra, ok := ctx.Value(nativeProcessEnvironmentKey{}).([]string); ok {
		cmd.Env = append(cmd.Env, extra...)
	}
	cmd.Stdin = bytes.NewReader(stdin)
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		r.Err = err
		return
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		r.Err = err
		return
	}
	defer stderrR.Close()
	defer stderrW.Close()
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW
	if err := cmd.Start(); err != nil {
		r.Err = err
		return
	}
	stdoutW.Close()
	stderrW.Close()
	var stdout, stderr browserOutputBuffer
	drained := make(chan error, 2)
	go func() { _, err := io.Copy(&stdout, stdoutR); drained <- err }()
	go func() { _, err := io.Copy(&stderr, stderrR); drained <- err }()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	stop := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	select {
	case err = <-waited:
	case <-ctx.Done():
		stop()
		err = <-waited
		r.Err = ctx.Err()
	}
	// bwrap's PID namespace also reaps detached descendants on normal exit.
	stop()
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		r.Err = errors.Join(r.Err, fmt.Errorf("native process exit: %w", err))
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for left := 2; left > 0; {
		select {
		case err := <-drained:
			left--
			if err != nil {
				r.Err = errors.Join(r.Err, err)
			}
		case <-timer.C:
			stdoutR.Close()
			stderrR.Close()
			r.Err = errors.Join(r.Err, errors.New("native output drain timed out"))
		}
	}
	if err := ctx.Err(); err != nil {
		r.Err = errors.Join(r.Err, err)
		r.TimedOut = errors.Is(err, context.DeadlineExceeded)
	}
	r.Stdout, r.StdoutTruncated = stdout.output()
	r.Stderr, r.StderrTruncated = stderr.output()
	if r.ExitCode != 0 && strings.HasPrefix(strings.TrimSpace(r.Stderr), "bwrap:") {
		// Isolation setup did not establish a target-process outcome. Do not
		// mislabel a missing namespace/mount permission as a failing test.
		r.ExitCode = -1
		r.Err = errors.Join(r.Err, errors.New("native isolation could not start"))
	}
	return
}

func nativeProcessEnvironment(env []string) []string {
	remove := map[string]bool{"NODE_OPTIONS": true, "NODE_PATH": true, "BASH_ENV": true, "ENV": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true,
		"GOCACHE": true, "GOTMPDIR": true, "TMPDIR": true, "GOTOOLCHAIN": true, "GOPROXY": true, "GOSUMDB": true, "GOENV": true, "GOWORK": true, "GOFLAGS": true}
	var clean []string
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if !remove[key] {
			clean = append(clean, item)
		}
	}
	return append(clean, "GOCACHE=/tmp/cerveau-check-cache", "GOTMPDIR=/tmp/cerveau-check-tmp", "TMPDIR=/tmp", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOENV=off", "GOWORK=off", "GOFLAGS=")
}
