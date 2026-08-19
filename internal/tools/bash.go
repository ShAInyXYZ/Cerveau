package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	// Long enough for real build/install commands (npm install, cargo build, etc.)
	// which routinely exceed 30s on a cold project.
	bashTimeout  = 180 * time.Second
	bashCapChars = 8000
)

type Bash struct {
	dir string
}

func NewBash(workspaceRoot string) *Bash {
	return &Bash{dir: newJail(workspaceRoot).root}
}

func (t *Bash) Name() string { return "bash" }

func (t *Bash) Description() string {
	return "Run a one-shot shell command in the workspace (30s–3min, output capped). " +
		"Do NOT start long-lived servers here (npm run dev, vite, watchers) — they never " +
		"return; the call will time out and be killed. Build/one-shot commands only."
}

func (t *Bash) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"required": []string{"command"},
	}
}

func (t *Bash) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Command == "" {
		return "", fmt.Errorf("command required")
	}

	// A server backgrounded with `&` dies with the process group the moment
	// this call returns — running it is ALWAYS wasted work (and often reports
	// success because the chain exits 0 before the kill). Refuse up front;
	// hint-after-failure proved too weak, the model burned turns on it.
	if backgroundsServer(a.Command) {
		return "", fmt.Errorf("refused: bash cannot host servers (the process group is killed when the call ends). For a STATIC site: use the serve tool. For a vite/npm DEV server: build once with bash (e.g. npx vite build), then serve the output with the serve tool: serve {\"action\":\"start\",\"dir\":\"<project>/dist\"}")
	}

	cmd := exec.Command("bash", "-c", a.Command)
	cmd.Dir = t.dir
	// Own process group so we can kill the WHOLE tree on timeout. A command that
	// backgrounds a long-lived child (e.g. `npm run dev &`) otherwise leaks that
	// child, which keeps the output pipe open and hangs Wait() forever — the
	// original bug: a dev server ran for 300s+ past the timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	killTree := func() {
		if cmd.Process != nil {
			// negative pid = the whole process group (children included)
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Process.Kill()
		}
	}

	var runErr error
	timedOut := false
	select {
	case runErr = <-done:
	case <-time.After(bashTimeout):
		timedOut = true
		killTree()
		<-done // reap
	case <-ctx.Done():
		killTree()
		<-done
		return capOutput(buf.String()), ctx.Err()
	}

	text := capOutput(buf.String())
	if timedOut {
		if looksLikeServer(a.Command) {
			return text, fmt.Errorf("command timed out — this looks like a long-lived server, which bash cannot run (it is killed when the call ends). For a STATIC site: use the serve tool. For a vite/npm DEV server: build once (npx vite build), then serve the output: serve {\"action\":\"start\",\"dir\":\"<project>/dist\"}")
		}
		return text, fmt.Errorf("command timed out after %s", bashTimeout)
	}
	if runErr != nil {
		// A backgrounded server (`... &`) is killed with the process group the
		// moment this call ends — point the model at the serve tool instead of
		// letting it retry the same doomed command.
		if strings.Contains(a.Command, "&") && looksLikeServer(a.Command) {
			return text, fmt.Errorf("exit: %w — a backgrounded server cannot survive a bash call (its process group is killed). Use the `serve` tool to run a static server instead", runErr)
		}
		// A non-zero exit is NOT a tool failure. Test runners, linters, type
		// checkers and grep all exit non-zero to REPORT something — the command
		// ran exactly as asked and the answer was "no". Returning a Go error
		// made the loop count each one toward the guard threshold, so a model
		// that wrote a verification script and iterated on what it found was
		// stopped for doing the right thing (a real DAW benchmark died this way
		// on its third passing-but-reporting-failures Playwright run).
		//
		// The exit status still has to be VISIBLE or the model cannot tell a
		// failing suite from a passing one, so it goes into the output text.
		// Errors are reserved for commands that could not run at all — those
		// are handled by cmd.Start() above and by exec.ErrNotFound below.
		if isCommandNotFound(runErr, text) {
			return text, fmt.Errorf("exit: %w", runErr)
		}
		if text == "" {
			text = "(no output)"
		}
		return text + "\n\n[exit: " + runErr.Error() + "]", nil
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

// isCommandNotFound distinguishes "the command ran and said no" from "there was
// no command to run". Only the latter is a tool error the guard should count.
func isCommandNotFound(runErr error, output string) bool {
	if errors.Is(runErr, exec.ErrNotFound) {
		return true
	}
	// bash reports these itself and exits 127 / 126
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		switch ee.ExitCode() {
		case 127, 126: // not found / not executable
			return true
		}
	}
	return false
}

func capOutput(text string) string {
	if len(text) <= bashCapChars {
		return text
	}
	// Keep the HEAD and the TAIL. A build or test failure prints its error on
	// the last lines, so head-only truncation would throw away exactly what
	// the model needs to self-correct. Weight toward the tail.
	head := bashCapChars / 3
	tail := bashCapChars - head
	// Trim to line boundaries so neither fragment starts/ends mid-line.
	h := text[:head]
	if i := strings.LastIndexByte(h, '\n'); i > 0 {
		h = h[:i]
	}
	tStart := len(text) - tail
	tl := text[tStart:]
	if i := strings.IndexByte(tl, '\n'); i >= 0 && i < len(tl)-1 {
		tl = tl[i+1:]
	}
	return h + fmt.Sprintf("\n...[%d bytes omitted — showing the start and end]...\n", len(text)-len(h)-len(tl)) + tl
}

// backgroundsServer reports whether the command starts a server detached with
// a single `&` (job control) — the doomed pattern. `&&`, `2>&1` and `>&` are
// not background operators and must stay allowed.
func backgroundsServer(cmd string) bool {
	if !looksLikeServer(cmd) {
		return false
	}
	return bgAmp.MatchString(cmd)
}

// a lone & at end-of-command or before a separator; not &&, not >&, not 2>&1
var bgAmp = regexp.MustCompile(`[^&>\d]&(\s*$|\s*;|\s+[^&])`)

// looksLikeServer spots commands that never return, so the timeout can explain
// itself instead of just "timed out".
func looksLikeServer(cmd string) bool {
	for _, s := range []string{"npm run dev", "npm start", "vite", "next dev", "serve", "--watch", "nodemon", "http-server", "http.server", "python -m http", "python3 -m http", "live-server"} {
		if bytes.Contains([]byte(cmd), []byte(s)) {
			return true
		}
	}
	return false
}
