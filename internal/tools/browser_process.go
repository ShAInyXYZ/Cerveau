package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const browserProcessOutputLimit = 128 * 1024

type browserProcessResult struct {
	Stdout, Stderr                   string
	Err                              error
	TimedOut                         bool
	Elapsed                          time.Duration
	StdoutTruncated, StderrTruncated bool
}

// runBrowserProcess owns exactly one new process group and one temporary
// profile. It never attaches to an existing browser or cleans another profile.
// The surrounding tools already target Unix process groups (see bash.go).
func runBrowserProcess(ctx context.Context, chrome string, args []string) (result browserProcessResult) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if err := ctx.Err(); err != nil {
		result.Err = err
		result.TimedOut = errors.Is(err, context.DeadlineExceeded)
		return
	}
	for _, arg := range args {
		if arg == "--user-data-dir" || strings.HasPrefix(arg, "--user-data-dir=") {
			result.Err = errors.New("browser process requires its own temporary user-data-dir")
			return
		}
	}
	profile, err := os.MkdirTemp("", "crv-browser-")
	if err != nil {
		result.Err = fmt.Errorf("create browser profile: %w", err)
		return
	}
	defer func() {
		// profile is the exact freshly created directory, never a caller path.
		if err := os.RemoveAll(profile); err != nil {
			result.Err = errors.Join(result.Err, fmt.Errorf("remove browser profile: %w", err))
		}
	}()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		result.Err = fmt.Errorf("create browser stdout pipe: %w", err)
		return
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		result.Err = fmt.Errorf("create browser stderr pipe: %w", err)
		return
	}
	defer stderrR.Close()
	defer stderrW.Close()

	privateArgs := append(append([]string(nil), args...), "--user-data-dir="+profile)
	cmd := exec.Command(chrome, privateArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Real file handles keep os/exec.Wait independent of pipe-copy goroutines:
	// a descendant holding a pipe must not hide the direct browser's exit.
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW
	if err := cmd.Start(); err != nil {
		result.Err = fmt.Errorf("launch browser: %w", err)
		if ctxErr := ctx.Err(); ctxErr != nil {
			result.Err = errors.Join(ctxErr, result.Err)
			result.TimedOut = errors.Is(ctxErr, context.DeadlineExceeded)
		}
		return
	}
	_ = stdoutW.Close()
	_ = stderrW.Close()

	var stdout, stderr browserOutputBuffer
	outputDone := make(chan error, 2)
	go func() { _, err := io.Copy(&stdout, stdoutR); outputDone <- err }()
	go func() { _, err := io.Copy(&stderr, stderrR); outputDone <- err }()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	groupStopped := false
	stopOwnedGroup := func() error {
		if groupStopped {
			return nil
		}
		groupStopped = true
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			// Only our exact direct child is a fallback target.
			_ = cmd.Process.Kill()
			return fmt.Errorf("stop browser process group: %w", err)
		}
		return nil
	}
	var waitErr error
	select {
	case waitErr = <-waitDone:
	case <-ctx.Done():
		result.Err = errors.Join(ctx.Err(), stopOwnedGroup())
		waitErr = <-waitDone
	}
	if waitErr != nil {
		result.Err = errors.Join(result.Err, fmt.Errorf("browser exit: %w", waitErr))
	}
	// Even a clean direct-parent exit can leave browser descendants running.
	result.Err = errors.Join(result.Err, stopOwnedGroup())

	// Drain buffered evidence after group cleanup. A process escaping its group
	// cannot make a surviving inherited pipe block this tool indefinitely.
	drainTimer := time.NewTimer(time.Second)
	defer drainTimer.Stop()
	for remaining := 2; remaining > 0; {
		select {
		case err := <-outputDone:
			remaining--
			if err != nil {
				result.Err = errors.Join(result.Err, fmt.Errorf("read browser output: %w", err))
			}
		case <-drainTimer.C:
			result.Err = errors.Join(result.Err, errors.New("browser output drain timed out"))
			_ = stdoutR.Close()
			_ = stderrR.Close()
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		result.Err = errors.Join(ctxErr, result.Err)
		result.TimedOut = errors.Is(ctxErr, context.DeadlineExceeded)
	}
	result.Stdout, result.StdoutTruncated = stdout.output()
	result.Stderr, result.StderrTruncated = stderr.output()
	return
}

// browserOutputBuffer retains the beginning and end of a stream while bounding
// retained bytes regardless of the child's output volume. Each instance has
// one writer; output is called only after its writer completes.
type browserOutputBuffer struct {
	head, tail []byte
	total      uint64
}

func (b *browserOutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.total += uint64(n)
	headLimit := browserProcessOutputLimit / 2
	tailLimit := browserProcessOutputLimit - headLimit
	if len(b.head) < headLimit {
		count := min(len(p), headLimit-len(b.head))
		b.head = append(b.head, p[:count]...)
		p = p[count:]
	}
	if len(p) >= tailLimit {
		b.tail = append(b.tail[:0], p[len(p)-tailLimit:]...)
	} else {
		if excess := len(b.tail) + len(p) - tailLimit; excess > 0 {
			copy(b.tail, b.tail[excess:])
			b.tail = b.tail[:len(b.tail)-excess]
		}
		b.tail = append(b.tail, p...)
	}
	return n, nil
}

func (b *browserOutputBuffer) output() (string, bool) {
	if b.total <= browserProcessOutputLimit {
		return string(b.head) + string(b.tail), false
	}
	const marker = "\n...[browser output truncated; showing start and end]...\n"
	headLen := (browserProcessOutputLimit - len(marker)) / 2
	tailLen := browserProcessOutputLimit - len(marker) - headLen
	return string(b.head[:headLen]) + marker + string(b.tail[len(b.tail)-tailLen:]), true
}
