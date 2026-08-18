// crvcli — thin CLI client over the running crv HTTP API.
//
// It runs NO models and holds NO state; it just talks to a crv server
// (default http://localhost:7700, override with -addr or CRV_ADDR).
//
//	crvcli ask "refactor the parser"          # one-shot: new session, chat, print reply
//	crvcli ask -mode brainstorming "research X"
//	crvcli -workspace ~/code/app ask "add tests"   # work in a specific project
//
// The workspace belongs to the project: every session in it shares that path.
// The CLI is INDEPENDENT of the panel — it defaults to the directory you are
// standing in, and the panel's folder picker never redirects a session the
// terminal started.
//
//	crvcli -session sess_123 ask "continue"    # chat into an existing session
//	crvcli sessions                            # list sessions
//	crvcli new "my task"                       # create a session, print its id
//	crvcli health                              # component readiness
//	echo "prompt from stdin" | crvcli ask -
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", envOr("CRV_ADDR", "http://localhost:7700"), "crv server base URL")
	session := flag.String("session", "", "existing session id (default: create a fresh one)")
	mode := flag.String("mode", "", "chat mode: discussion | brainstorming | autopilot")
	workspace := flag.String("workspace", "", "project folder to work in (default: current directory)")
	jsonOut := flag.Bool("json", false, "print raw JSON responses")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	c := &client{base: strings.TrimRight(*addr, "/"), json: *jsonOut}

	var err error
	switch args[0] {
	case "ask":
		err = c.ask(*session, *mode, *workspace, args[1:])
	case "new":
		err = c.newSession(*workspace, args[1:])
	case "sessions":
		err = c.sessions()
	case "health":
		err = c.health()
	case "rfx":
		err = c.cmdRfx(args[1:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// resolveWorkspace decides which folder a CLI session works in.
//
// The CLI is independent of the panel. The panel picks a workspace with a
// folder dialog and writes the core's GLOBAL setting; a terminal caller has no
// picker, so it states the path itself — and defaults to the directory the
// user is standing in, which is what `cd project && crvcli ask ...` obviously
// means. Without this a CLI session silently inherited whatever folder the
// panel last pointed at.
//
// The path is made absolute HERE, because the server resolves relative paths
// from its own working directory, not the caller's.
func resolveWorkspace(ws string) (string, error) {
	if strings.TrimSpace(ws) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("workspace: cannot read the current directory: %w", err)
		}
		ws = cwd
	}
	abs, err := filepath.Abs(ws)
	if err != nil {
		return "", fmt.Errorf("workspace %q: %w", ws, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("workspace %s: %v", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %s is not a directory", abs)
	}
	return abs, nil
}

// clientTimeout bounds one chat turn. A fast MoE finishes a build task well
// inside 10 minutes; a dense model at a quarter of the speed does not.
func clientTimeout() time.Duration {
	if v := os.Getenv("CRV_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		fmt.Fprintf(os.Stderr, "ignoring CRV_TIMEOUT=%q (want e.g. 45m)\n", v)
	}
	return 10 * time.Minute
}

type client struct {
	base string
	json bool
}

// ask: (optionally create a session), send one chat turn, print the reply.
func (c *client) ask(session, mode, workspace string, rest []string) error {
	text, err := promptText(rest)
	if err != nil {
		return err
	}
	if session == "" {
		ws, err := resolveWorkspace(workspace)
		if err != nil {
			return err
		}
		m, err := c.post("/api/sessions", map[string]string{
			"name": firstLine(text, 40), "workspace": ws})
		if err != nil {
			return err
		}
		session = str(m["id"])
		if session == "" {
			return fmt.Errorf("could not create session: %v", m)
		}
		fmt.Fprintln(os.Stderr, "session:", session)
	}
	res, err := c.post("/api/sessions/"+session+"/chat", map[string]string{"text": text, "mode": mode})
	if err != nil {
		return err
	}
	if c.json {
		return printJSON(res)
	}
	fmt.Println(str(res["reply"]))
	if capped, _ := res["capped"].(bool); capped {
		fmt.Fprintf(os.Stderr, "[stopped: %s after %v iterations]\n", str(res["stop_reason"]), res["iterations"])
	}
	return nil
}

func (c *client) newSession(workspace string, rest []string) error {
	name := strings.TrimSpace(strings.Join(rest, " "))
	if name == "" {
		return fmt.Errorf("usage: crvcli new <name>")
	}
	ws, err := resolveWorkspace(workspace)
	if err != nil {
		return err
	}
	m, err := c.post("/api/sessions", map[string]string{"name": name, "workspace": ws})
	if err != nil {
		return err
	}
	if c.json {
		return printJSON(m)
	}
	fmt.Println(str(m["id"]))
	return nil
}

func (c *client) sessions() error {
	m, err := c.get("/api/sessions")
	if err != nil {
		return err
	}
	if c.json {
		return printJSON(m)
	}
	list, _ := m["sessions"].([]any)
	if len(list) == 0 {
		fmt.Println("(no sessions)")
		return nil
	}
	for _, s := range list {
		sm, _ := s.(map[string]any)
		fmt.Printf("%-24s  %s\n", str(sm["id"]), str(sm["name"]))
	}
	return nil
}

func (c *client) health() error {
	m, err := c.get("/api/health")
	if err != nil {
		return err
	}
	if c.json {
		return printJSON(m)
	}
	comps, _ := m["components"].(map[string]any)
	if len(comps) == 0 {
		return printJSON(m)
	}
	for name, v := range comps {
		fmt.Printf("%-12s %v\n", name, v)
	}
	return nil
}

// --- HTTP helpers ---

func (c *client) get(path string) (map[string]any, error) {
	return c.do("GET", path, nil)
}

func (c *client) post(path string, body any) (map[string]any, error) {
	return c.do("POST", path, body)
}

func (c *client) do(method, path string, body any) (map[string]any, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	// A chat turn takes as long as the model needs. 10 minutes suits a fast
	// MoE; a dense model at a quarter of the speed blows through it on a real
	// build task, and the client then reports a timeout as though the SERVER
	// were down — sending the user to check a healthy service. Configurable,
	// and the message below says what was actually observed.
	hc := &http.Client{Timeout: clientTimeout()}
	resp, err := hc.Do(req)
	if err != nil {
		// Say what was OBSERVED. A turn that outran the client timeout is not
		// the same as a server that is down, and telling the user to check a
		// healthy service sends them chasing the wrong thing.
		if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
			return nil, fmt.Errorf("no reply from crv at %s within %s — the turn may still be running; "+
				"raise the limit with CRV_TIMEOUT (e.g. CRV_TIMEOUT=45m)", c.base, clientTimeout())
		}
		return nil, fmt.Errorf("could not connect to crv at %s: %w", c.base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("bad response (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
	}
	if resp.StatusCode >= 400 {
		if e := str(m["error"]); e != "" {
			return nil, fmt.Errorf("%s (%d)", e, resp.StatusCode)
		}
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return m, nil
}

// --- utils ---

func promptText(rest []string) (string, error) {
	joined := strings.TrimSpace(strings.Join(rest, " "))
	if joined == "" || joined == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		joined = strings.TrimSpace(string(b))
	}
	if joined == "" {
		return "", fmt.Errorf("no prompt given (pass text or pipe via stdin)")
	}
	return joined, nil
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max]
	}
	return strings.TrimSpace(s)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func printJSON(v any) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `crvcli — thin CLI over a running crv server

usage:
  crvcli [flags] <command> [args]

commands:
  ask <text|->      one-shot chat; creates a session unless -session is given
  new <name>        create a session, print its id
  sessions          list sessions
  health            show component readiness
  rfx <sub>         reflex management: list | show | install | remove | test
                    (local, no server needed — see: crvcli rfx)

flags:
  -addr    crv base URL (default http://localhost:7700, or $CRV_ADDR)
  -session existing session id to chat into
  -mode    discussion | brainstorming | autopilot
  -workspace  project folder to work in (default: the current directory)
  -json    print raw JSON

the workspace:
  belongs to the PROJECT — every session in it shares that path. The CLI is
  independent of the panel: it uses the folder you are standing in unless you
  say otherwise, and the panel's picker never redirects a session started here.

examples:
  crvcli ask "explain internal/loop/loop.go"
  crvcli ask -mode brainstorming "research vector db options"
  crvcli -workspace ~/code/app ask "add tests for the parser"
  cd ~/code/app && crvcli ask "add tests"      # same thing
  crvcli -session sess_abc ask "continue"
  echo "prompt" | crvcli ask -
`)
}
