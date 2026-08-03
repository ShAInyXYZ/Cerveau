package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Serve runs long-lived static HTTP servers for the workspace — the one thing
// bash deliberately cannot do (bash kills its whole process group when the
// call returns, so a backgrounded server dies instantly). A serve server is
// an in-process http.Server that outlives the tool call; the agent starts it,
// gets a URL back immediately, and can stop it later.
//
// It serves static files ONLY, jailed to the workspace (http.FileServer over
// the jail root) — no code execution, so it's a safe-tier tool.
type Serve struct {
	root string
	mu   sync.Mutex
	srv  map[int]*http.Server // port -> running server
}

func NewServe(workspaceRoot string) *Serve {
	return &Serve{root: newJail(workspaceRoot).root, srv: map[int]*http.Server{}}
}

func (t *Serve) Name() string { return "serve" }

func (t *Serve) Description() string {
	return "Start a local static web server for the workspace so the user can open a page in their browser " +
		"(this is how you run an HTML/JS app — bash cannot, its background processes are killed). " +
		"action: start | stop | list. Returns the URL. Static files only."
}

func (t *Serve) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"start", "stop", "list"}, "description": "start a server, stop one, or list running ones"},
			"port":   map[string]any{"type": "integer", "description": "port (default 8000). For stop, the port to stop"},
			"dir":    map[string]any{"type": "string", "description": "workspace-relative subdirectory to serve as the root (default the workspace root)"},
		},
		"required": []string{"action"},
	}
}

func (t *Serve) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		Port   int    `json:"port"`
		Dir    string `json:"dir"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	switch a.Action {
	case "start":
		return t.start(a.Port, a.Dir)
	case "stop":
		return t.stop(a.Port)
	case "list":
		return t.list(), nil
	default:
		return "", fmt.Errorf("action must be start, stop, or list (got %q)", a.Action)
	}
}

func (t *Serve) start(port int, dir string) (string, error) {
	if port == 0 {
		port = 8000
	}
	t.mu.Lock()
	if _, ok := t.srv[port]; ok {
		t.mu.Unlock()
		return fmt.Sprintf("a server is already running at http://127.0.0.1:%d — open it, or stop it first", port), nil
	}
	t.mu.Unlock()

	// resolve the serve root inside the jail
	root := t.root
	if dir != "" {
		full, err := jail{root: t.root}.resolve(dir)
		if err != nil {
			return "", err
		}
		root = full
	}

	// bind first so we can report a real failure (port in use) synchronously
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", fmt.Errorf("cannot bind port %d: %w — try another port", port, err)
	}

	srv := &http.Server{Handler: http.FileServer(http.Dir(root))}
	t.mu.Lock()
	t.srv[port] = srv
	t.mu.Unlock()

	// the server lives in a goroutine — it OUTLIVES this tool call, which is
	// the whole point. It stays up until stop, or process exit.
	go func() {
		_ = srv.Serve(ln)
		t.mu.Lock()
		delete(t.srv, port)
		t.mu.Unlock()
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	return fmt.Sprintf("serving %s at %s — open it in your browser. Stop with serve action=stop port=%d.", dirLabel(dir), url, port), nil
}

func (t *Serve) stop(port int) (string, error) {
	if port == 0 {
		return "", fmt.Errorf("stop needs a port (see serve action=list)")
	}
	t.mu.Lock()
	srv, ok := t.srv[port]
	t.mu.Unlock()
	if !ok {
		return fmt.Sprintf("no server running on port %d", port), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	t.mu.Lock()
	delete(t.srv, port)
	t.mu.Unlock()
	return fmt.Sprintf("stopped the server on port %d", port), nil
}

func (t *Serve) list() string {
	t.mu.Lock()
	ports := make([]int, 0, len(t.srv))
	for p := range t.srv {
		ports = append(ports, p)
	}
	t.mu.Unlock()
	if len(ports) == 0 {
		return "no servers running"
	}
	sort.Ints(ports)
	var b strings.Builder
	b.WriteString("running servers:\n")
	for _, p := range ports {
		fmt.Fprintf(&b, "  http://127.0.0.1:%d\n", p)
	}
	return strings.TrimRight(b.String(), "\n")
}

// stopAll shuts every server down — used at process teardown and in tests.
func (t *Serve) stopAll() {
	t.mu.Lock()
	srvs := make([]*http.Server, 0, len(t.srv))
	for _, s := range t.srv {
		srvs = append(srvs, s)
	}
	t.srv = map[int]*http.Server{}
	t.mu.Unlock()
	for _, s := range srvs {
		_ = s.Close()
	}
}

func dirLabel(dir string) string {
	if dir == "" {
		return "the workspace"
	}
	return dir
}
