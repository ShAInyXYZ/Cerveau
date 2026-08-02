package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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
		hint := ""
		if looksLikeServer(a.Command) {
			hint = " — looks like a long-lived server; start it in the background or use a one-shot build instead"
		}
		return text, fmt.Errorf("command timed out after %s%s", bashTimeout, hint)
	}
	if runErr != nil {
		return text, fmt.Errorf("exit: %w", runErr)
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
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

// looksLikeServer spots commands that never return, so the timeout can explain
// itself instead of just "timed out".
func looksLikeServer(cmd string) bool {
	for _, s := range []string{"npm run dev", "npm start", "vite", "next dev", "serve", "--watch", "nodemon", "http-server"} {
		if bytes.Contains([]byte(cmd), []byte(s)) {
			return true
		}
	}
	return false
}
