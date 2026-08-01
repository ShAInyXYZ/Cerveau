package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"cerveau/internal/rfx"
)

// ExecReflexTool is a Synapse exec reflex (spec §4): an external program in
// any language, invoked as argv (NO shell), JSON args on stdin, JSON result
// on stdout, card-scoped environment. GBNF, guard, and ingress caps are
// inherited from the registry like every native tool.
type ExecReflexTool struct {
	def rfx.Reflex
	dir string
}

func (t *ExecReflexTool) Name() string        { return t.def.Name }
func (t *ExecReflexTool) Description() string { return t.def.Description }

func (t *ExecReflexTool) Schema() map[string]any {
	if t.def.Params != nil {
		return t.def.Params
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

const execDefaultTimeout = 60 * time.Second

func (t *ExecReflexTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	params := map[string]any{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return "", fmt.Errorf("exec %s: bad args JSON: %w", t.def.Name, err)
		}
	}
	if err := validateReflexArgs(t.def.Params, params); err != nil {
		return "", fmt.Errorf("exec %s: %w", t.def.Name, err)
	}

	// Substitute placeholders in argv — literal, no shell, no quoting layer
	// (spec §4: there is no shell to inject into).
	argv := make([]string, len(t.def.Argv))
	for i, a := range t.def.Argv {
		out, err := substituteString(a, params, nil, false)
		if err != nil {
			return "", fmt.Errorf("exec %s argv[%d]: %w", t.def.Name, i, err)
		}
		argv[i] = out
	}

	timeout := execDefaultTimeout
	if t.def.Timeout != "" {
		d, err := time.ParseDuration(t.def.Timeout)
		if err == nil && d > 0 {
			timeout = d
		}
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = t.dir
	cmd.Env = scrubbedEnv(t.def.Card.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // same kill-tree discipline as bash

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("exec %s: stdin: %w", t.def.Name, err)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("exec %s: start %s: %w", t.def.Name, argv[0], err)
	}
	go func() {
		json.NewEncoder(stdin).Encode(params)
		stdin.Close()
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	killTree := func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Process.Kill()
		}
	}

	var runErr error
	timedOut := false
	select {
	case runErr = <-done:
	case <-time.After(timeout):
		timedOut = true
		killTree()
		<-done
	case <-ctx.Done():
		killTree()
		<-done
		return "", ctx.Err()
	}

	out := parseExecOutput(stdout.Bytes())
	if timedOut {
		return out, fmt.Errorf("exec %s: timed out after %s (killed the whole process group)", t.def.Name, timeout)
	}
	if runErr != nil {
		// stderr is KEPT — the model debugs with real text, never "exit status 1".
		if s := stderr.String(); s != "" {
			return out + s, fmt.Errorf("exec %s: %w", t.def.Name, runErr)
		}
		return out, fmt.Errorf("exec %s: %w", t.def.Name, runErr)
	}
	if out == "" {
		return "(no output)", nil
	}
	return out, nil
}

// parseExecOutput: a JSON object with a string "output" field wins (spec §4);
// anything else is the output verbatim.
func parseExecOutput(raw []byte) string {
	var v struct {
		Output string `json:"output"`
	}
	if json.Unmarshal(raw, &v) == nil && v.Output != "" {
		return v.Output
	}
	return string(raw)
}

// scrubbedEnv: PATH, HOME, LANG plus exactly the card's allowlisted names
// (spec §5). Nothing else leaks into the subprocess — no API keys by ambient
// inheritance.
func scrubbedEnv(allow []string) []string {
	names := append([]string{"PATH", "HOME", "LANG"}, allow...)
	var env []string
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok {
			env = append(env, n+"="+v)
		}
	}
	return env
}
