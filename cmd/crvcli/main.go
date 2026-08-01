// crvcli — thin CLI client over the running crv HTTP API.
//
// It runs NO models and holds NO state; it just talks to a crv server
// (default http://localhost:7700, override with -addr or CRV_ADDR).
//
//	crvcli ask "refactor the parser"          # one-shot: new session, chat, print reply
//	crvcli ask -mode brainstorming "research X"
//	crvcli -session sess_123 ask "continue"    # chat into an existing session
//	crvcli sessions                            # list sessions
//	crvcli new "my task"                       # create a session, print its id
//	crvcli health                              # component readiness
//	echo "prompt from stdin" | crvcli ask -
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", envOr("CRV_ADDR", "http://localhost:7700"), "crv server base URL")
	session := flag.String("session", "", "existing session id (default: create a fresh one)")
	mode := flag.String("mode", "", "chat mode: discussion | brainstorming | autopilot")
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
		err = c.ask(*session, *mode, args[1:])
	case "new":
		err = c.newSession(args[1:])
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

type client struct {
	base string
	json bool
}

// ask: (optionally create a session), send one chat turn, print the reply.
func (c *client) ask(session, mode string, rest []string) error {
	text, err := promptText(rest)
	if err != nil {
		return err
	}
	if session == "" {
		m, err := c.post("/api/sessions", map[string]string{"name": firstLine(text, 40)})
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

func (c *client) newSession(rest []string) error {
	name := strings.TrimSpace(strings.Join(rest, " "))
	if name == "" {
		return fmt.Errorf("usage: crvcli new <name>")
	}
	m, err := c.post("/api/sessions", map[string]string{"name": name})
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
	hc := &http.Client{Timeout: 10 * time.Minute} // chat turns can be long
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach crv at %s (%w) — is the server running?", c.base, err)
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
  -json    print raw JSON

examples:
  crvcli ask "explain internal/loop/loop.go"
  crvcli ask -mode brainstorming "research vector db options"
  crvcli -session sess_abc ask "continue"
  echo "prompt" | crvcli ask -
`)
}
