package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func browserFixtureArgs(mode string) []string {
	return []string{"-test.run=^TestBrowserProcessFixture$", "--", "--browser-process-fixture", mode}
}

// Test binary subprocesses simulate Chromium without loading a browser, a
// benchmark, the network, or Core. Child mode deliberately inherits its parent's
// pipes and process group to exercise descendant cleanup.
func TestBrowserProcessFixture(t *testing.T) {
	mode := ""
	for i, arg := range os.Args {
		if arg == "--browser-process-fixture" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
		}
	}
	if mode == "" {
		return
	}
	profile := ""
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--user-data-dir=") {
			profile = strings.TrimPrefix(arg, "--user-data-dir=")
		}
	}
	if mode == "child" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if profile == "" {
		fmt.Fprintln(os.Stderr, "missing isolated profile")
		os.Exit(91)
	}
	if err := os.WriteFile(filepath.Join(profile, "fixture-owned"), []byte("temporary browser state"), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(92)
	}
	fmt.Fprintln(os.Stdout, "PROFILE="+profile)
	switch mode {
	case "success":
		fmt.Fprintln(os.Stdout, "<html><body>ok</body></html>")
		fmt.Fprintln(os.Stderr, "browser diagnostic")
	case "failure":
		fmt.Fprintln(os.Stdout, "partial DOM")
		fmt.Fprintln(os.Stderr, "renderer failed")
		os.Exit(17)
	case "large":
		fmt.Fprint(os.Stdout, "OUT-START"+strings.Repeat("o", 3*browserProcessOutputLimit)+"OUT-END")
		fmt.Fprint(os.Stderr, "ERR-START"+strings.Repeat("e", 3*browserProcessOutputLimit)+"ERR-END")
	case "hang", "orphan":
		child := exec.Command(os.Args[0], browserFixtureArgs("child")...)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(93)
		}
		fmt.Fprintln(os.Stdout, "CHILD="+strconv.Itoa(child.Process.Pid))
		fmt.Fprintln(os.Stderr, "partial diagnostics before exit or timeout")
		if mode == "hang" {
			for {
				time.Sleep(time.Hour)
			}
		}
	}
	os.Exit(0)
}

func browserFixtureField(output, key string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"=")
		}
	}
	return ""
}

func assertBrowserProfileRemoved(t *testing.T, output string) {
	t.Helper()
	profile := browserFixtureField(output, "PROFILE")
	if profile == "" {
		t.Fatalf("fixture did not report its temporary profile: %q", output)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("temporary profile was not removed: %s: %v", profile, err)
	}
}

func assertBrowserChildStopped(t *testing.T, output string) {
	t.Helper()
	pid, err := strconv.Atoi(browserFixtureField(output, "CHILD"))
	if err != nil || pid <= 1 {
		t.Fatalf("fixture child PID missing: %q", output)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		// Linux may retain a killed orphan as a zombie until its reaper runs.
		// A zombie cannot execute or retain the browser pipes/profile handles.
		if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
			end := strings.LastIndexByte(string(raw), ')')
			if end >= 0 && strings.HasPrefix(string(raw[end+1:]), " Z") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Only the exact child created by this test is eligible for fallback cleanup.
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("browser descendant %d survived process cleanup", pid)
}

func TestBrowserProcessPreservesSuccessAndNonzeroFailure(t *testing.T) {
	for _, mode := range []string{"success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got := runBrowserProcess(ctx, os.Args[0], browserFixtureArgs(mode))
			if got.TimedOut || got.Elapsed <= 0 || got.StdoutTruncated || got.StderrTruncated {
				t.Fatalf("incorrect completion metadata: %+v", got)
			}
			if mode == "success" {
				if got.Err != nil || !strings.Contains(got.Stdout, "<html>") || !strings.Contains(got.Stderr, "browser diagnostic") {
					t.Fatalf("successful browser output lost: %+v", got)
				}
			} else {
				var exit *exec.ExitError
				if !errors.As(got.Err, &exit) || exit.ExitCode() != 17 || !strings.Contains(got.Stdout, "partial DOM") || !strings.Contains(got.Stderr, "renderer failed") {
					t.Fatalf("partial output must not convert nonzero exit to success: %+v", got)
				}
			}
			assertBrowserProfileRemoved(t, got.Stdout)
		})
	}
}

func TestBrowserProcessBoundsBothStreamsAndRetainsEnds(t *testing.T) {
	got := runBrowserProcess(context.Background(), os.Args[0], browserFixtureArgs("large"))
	if got.Err != nil || !got.StdoutTruncated || !got.StderrTruncated || len(got.Stdout) > browserProcessOutputLimit || len(got.Stderr) > browserProcessOutputLimit {
		t.Fatalf("output not bounded at %d bytes per stream: stdout=%d stderr=%d %+v", browserProcessOutputLimit, len(got.Stdout), len(got.Stderr), got.Err)
	}
	for _, want := range []string{"OUT-START", "OUT-END", "truncated"} {
		if !strings.Contains(got.Stdout, want) {
			t.Errorf("stdout lost %q", want)
		}
	}
	for _, want := range []string{"ERR-START", "ERR-END", "truncated"} {
		if !strings.Contains(got.Stderr, want) {
			t.Errorf("stderr lost %q", want)
		}
	}
	assertBrowserProfileRemoved(t, got.Stdout)
}

func TestBrowserProcessTimeoutCleansOnlyOwnedGroup(t *testing.T) {
	unrelated := exec.Command(os.Args[0], browserFixtureArgs("child")...)
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unrelated.Process.Kill(); unrelated.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	got := runBrowserProcess(ctx, os.Args[0], browserFixtureArgs("hang"))
	if !got.TimedOut || !errors.Is(got.Err, context.DeadlineExceeded) || got.Elapsed > 3*time.Second || !strings.Contains(got.Stderr, "partial diagnostics") {
		t.Fatalf("deadline or partial evidence was lost: %+v", got)
	}
	assertBrowserProfileRemoved(t, got.Stdout)
	assertBrowserChildStopped(t, got.Stdout)
	if err := unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("cleanup touched an unrelated process: %v", err)
	}
}

func TestBrowserProcessParentExitCleansInheritedPipeChildren(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := runBrowserProcess(ctx, os.Args[0], browserFixtureArgs("orphan"))
	if got.Err != nil || got.TimedOut || got.Elapsed > 3*time.Second {
		t.Fatalf("exited browser waited for a surviving pipe holder: %+v", got)
	}
	assertBrowserProfileRemoved(t, got.Stdout)
	assertBrowserChildStopped(t, got.Stdout)
}

func TestBrowserProcessLaunchFailureCancellationAndProfileOverride(t *testing.T) {
	profiles := t.TempDir()
	t.Setenv("TMPDIR", profiles)
	missing := filepath.Join(profiles, "missing-browser")
	if got := runBrowserProcess(context.Background(), missing, nil); got.Err == nil || got.TimedOut {
		t.Fatalf("launch failure lost: %+v", got)
	}
	if entries, err := os.ReadDir(profiles); err != nil || len(entries) != 0 {
		t.Fatalf("failed launch left a browser profile: %v %v", entries, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := runBrowserProcess(ctx, os.Args[0], browserFixtureArgs("success")); !errors.Is(got.Err, context.Canceled) || got.TimedOut || got.Stdout != "" {
		t.Fatalf("cancelled call launched browser or became success/deadline: %+v", got)
	}
	for _, arg := range []string{"--user-data-dir", "--user-data-dir=/tmp/not-owned"} {
		if got := runBrowserProcess(context.Background(), os.Args[0], append(browserFixtureArgs("success"), arg)); got.Err == nil || got.Stdout != "" {
			t.Fatalf("caller can escape private browser profile: %+v", got)
		}
	}
}

func TestBrowserProcessCancellationAfterLaunchRetainsEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(400*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	got := runBrowserProcess(ctx, os.Args[0], browserFixtureArgs("hang"))
	if got.TimedOut || !errors.Is(got.Err, context.Canceled) || got.Elapsed > 3*time.Second || !strings.Contains(got.Stderr, "partial diagnostics") {
		t.Fatalf("cancellation became success/deadline or lost evidence: %+v", got)
	}
	assertBrowserProfileRemoved(t, got.Stdout)
	assertBrowserChildStopped(t, got.Stdout)
}

func TestBrowserProcessOutputBufferBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, browserProcessOutputLimit / 2, browserProcessOutputLimit - 1, browserProcessOutputLimit, browserProcessOutputLimit + 1, 3 * browserProcessOutputLimit} {
		for _, chunkSize := range []int{4093, browserProcessOutputLimit * 4} {
			t.Run(fmt.Sprintf("size=%d/chunk=%d", size, chunkSize), func(t *testing.T) {
				input := strings.Repeat("0123456789", (size+9)/10)[:size]
				var capture browserOutputBuffer
				for start := 0; start < size; start += chunkSize {
					end := min(size, start+chunkSize)
					if n, err := capture.Write([]byte(input[start:end])); err != nil || n != end-start {
						t.Fatalf("short capture write: %d, %v", n, err)
					}
				}
				got, truncated := capture.output()
				if truncated != (size > browserProcessOutputLimit) || len(got) > browserProcessOutputLimit {
					t.Fatalf("bad truncation: input=%d output=%d flag=%v", size, len(got), truncated)
				}
				if !truncated && got != input {
					t.Fatal("untruncated output differs from input")
				}
				if truncated && (!strings.HasPrefix(got, input[:100]) || !strings.HasSuffix(got, input[len(input)-100:])) {
					t.Fatal("truncated output lost original start or end")
				}
			})
		}
	}
}

func TestBrowserProcessProfilesAreIsolatedAcrossConcurrentCalls(t *testing.T) {
	var wg sync.WaitGroup
	results := make([]browserProcessResult, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = runBrowserProcess(context.Background(), os.Args[0], browserFixtureArgs("success"))
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, got := range results {
		if got.Err != nil {
			t.Fatal(got.Err)
		}
		profile := browserFixtureField(got.Stdout, "PROFILE")
		if seen[profile] {
			t.Fatal("concurrent browser calls share a profile", profile)
		}
		seen[profile] = true
		assertBrowserProfileRemoved(t, got.Stdout)
	}
}
