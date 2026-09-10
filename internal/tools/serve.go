package tools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const serveProbeLimit = 1 << 20
const serveProbeTimeout = 5 * time.Second

// Serve runs long-lived static HTTP servers for the workspace — the one thing
// bash deliberately cannot do (bash kills its whole process group when the
// call returns, so a backgrounded server dies instantly). A serve server is
// an in-process http.Server that outlives the tool call; the agent starts it,
// gets a URL back immediately, and can stop it later.
//
// It serves static files ONLY. os.Root confines every file open, including
// symlink traversal, to the captured served directory rather than trusting a
// one-time lexical path check around http.Dir.
type Serve struct {
	root string
	mu   sync.Mutex
	srv  map[int]*servedStatic // port -> server owned by this workspace tool
}

type servedStatic struct {
	server           *http.Server
	files            *os.Root
	info             os.FileInfo
	id, origin, root string
	workspace        string
	mu               sync.RWMutex // a probe retains ownership until it finishes
	closed           bool
}

func NewServe(workspaceRoot string) *Serve {
	root := newJail(workspaceRoot).root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &Serve{root: root, srv: map[int]*servedStatic{}}
}

func (t *Serve) Name() string { return "serve" }

func (t *Serve) Description() string {
	return "Start a local static web server for the workspace so the user can open a page in their browser " +
		"(this is how you run an HTML/JS app — bash cannot, its background processes are killed). " +
		"action: start | stop | list | probe. Start/list report the exact served root and origin. " +
		"Probe an OWNED server using port and URL path to observe bounded HTTP status, MIME, bytes and file identity; " +
		"this checks delivery, not application behavior. Static files only."
}

func (t *Serve) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"start", "stop", "list", "probe"}, "description": "start a server, stop/list owned servers, or probe one owned server"},
			"port":   map[string]any{"type": "integer", "minimum": 0, "maximum": 65535, "description": "port (start default/zero means 8000). Required and nonzero for stop/probe"},
			"dir":    map[string]any{"type": "string", "description": "workspace-relative subdirectory to serve as the root (default the workspace root)"},
			"path":   map[string]any{"type": "string", "maxLength": 1024, "description": "probe only: URL path within the served root, default /. No URL, query, fragment or traversal; redirects are reported, not followed"},
			"method": map[string]any{"type": "string", "enum": []string{"GET", "HEAD"}, "description": "probe only: GET (default) or HEAD; at most 1 MiB and 5 seconds"},
		},
		"required": []string{"action"},
	}
}

func (t *Serve) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		Port   int    `json:"port"`
		Dir    string `json:"dir"`
		Path   string `json:"path"`
		Method string `json:"method"`
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
	case "probe":
		return t.probe(ctx, a.Port, a.Path, a.Method)
	default:
		return "", fmt.Errorf("action must be start, stop, list, or probe (got %q)", a.Action)
	}
}

func (t *Serve) start(port int, dir string) (string, error) {
	if port == 0 {
		port = 8000
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("port must be between 1 and 65535")
	}

	full, err := (jail{root: t.root}).resolve(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(t.root, full)
	if err != nil {
		return "", err
	}
	workspace, err := os.OpenRoot(t.root)
	if err != nil {
		return "", fmt.Errorf("open workspace: %w", err)
	}
	defer workspace.Close()
	files, err := workspace.OpenRoot(rel)
	if err != nil {
		return "", fmt.Errorf("open served directory: %w", err)
	}
	info, err := files.Stat(".")
	if err != nil {
		files.Close()
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		rel, _ = filepath.Rel(t.root, resolved)
	}
	rel = filepath.ToSlash(rel)
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.srv[port]; ok {
		files.Close()
		if !os.SameFile(old.info, info) {
			return "", fmt.Errorf("port %d already serves root %q, not requested root %q; stop it explicitly or use another port", port, old.root, rel)
		}
		return fmt.Sprintf("a server is already running at %s (root %q, server %s) — open it, or stop it first", old.origin, old.root, old.id), nil
	}

	// bind first so we can report a real failure (port in use) synchronously
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		files.Close()
		return "", fmt.Errorf("cannot bind port %d: %w — try another port", port, err)
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		ln.Close()
		files.Close()
		return "", fmt.Errorf("server identity: %w", err)
	}
	owned := &servedStatic{files: files, info: info, id: fmt.Sprintf("serve_%x", nonce), root: rel, workspace: t.root, origin: fmt.Sprintf("http://127.0.0.1:%d", port)}
	static := http.FileServerFS(serveRootFS{files})
	owned.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Cerveau-Serve-ID", owned.id)
			if !owned.rootCurrent() {
				http.Error(w, "served root identity changed; stop and restart this server", http.StatusConflict)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "static server accepts GET and HEAD only", http.StatusMethodNotAllowed)
				return
			}
			static.ServeHTTP(w, r)
		})}
	t.srv[port] = owned

	// the server lives in a goroutine — it OUTLIVES this tool call, which is
	// the whole point. It stays up until stop, or process exit.
	go func() {
		_ = owned.server.Serve(ln)
		t.mu.Lock()
		if t.srv[port] == owned {
			delete(t.srv, port)
		}
		t.mu.Unlock()
		owned.mu.Lock()
		owned.files.Close()
		owned.closed = true
		owned.mu.Unlock()
	}()

	return fmt.Sprintf("serving %s at %s (root %q, server %s) — open it in your browser. Stop with serve action=stop port=%d.", dirLabel(dir), owned.origin, owned.root, owned.id, port), nil
}

func (t *Serve) stop(port int) (string, error) {
	if port == 0 {
		return "", fmt.Errorf("stop needs a port (see serve action=list)")
	}
	t.mu.Lock()
	owned, ok := t.srv[port]
	if ok {
		delete(t.srv, port)
	}
	t.mu.Unlock()
	if !ok {
		return fmt.Sprintf("no server running on port %d", port), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	owned.mu.Lock()
	if err := owned.server.Shutdown(ctx); err != nil {
		_ = owned.server.Close()
	}
	owned.closed = true
	owned.mu.Unlock()
	return fmt.Sprintf("stopped the server on port %d", port), nil
}

func (t *Serve) list() string {
	t.mu.Lock()
	ports := make([]int, 0, len(t.srv))
	servers := make(map[int]*servedStatic, len(t.srv))
	for p := range t.srv {
		ports = append(ports, p)
		servers[p] = t.srv[p]
	}
	t.mu.Unlock()
	if len(ports) == 0 {
		return "no servers running"
	}
	sort.Ints(ports)
	var b strings.Builder
	b.WriteString("running servers:\n")
	for _, p := range ports {
		s := servers[p]
		fmt.Fprintf(&b, "  %s — root %q, server %s\n", s.origin, s.root, s.id)
	}
	return strings.TrimRight(b.String(), "\n")
}

// stopAll shuts every server down — used at process teardown and in tests.
func (t *Serve) stopAll() {
	t.mu.Lock()
	srvs := make([]*servedStatic, 0, len(t.srv))
	for _, s := range t.srv {
		srvs = append(srvs, s)
	}
	t.srv = map[int]*servedStatic{}
	t.mu.Unlock()
	for _, s := range srvs {
		s.mu.Lock()
		_ = s.server.Close()
		s.closed = true
		s.mu.Unlock()
	}
}

type serveProbeResult struct {
	Schema         string            `json:"schema"`
	Outcome        string            `json:"outcome"`
	ServerID       string            `json:"server_id"`
	Origin         string            `json:"origin"`
	Root           string            `json:"root"`
	Path           string            `json:"path"`
	Method         string            `json:"method"`
	Status         int               `json:"status,omitempty"`
	MIME           string            `json:"mime,omitempty"`
	Bytes          int               `json:"bytes"`
	BodyComplete   bool              `json:"body_complete"`
	ResponseSHA256 string            `json:"response_sha256,omitempty"`
	Source         *serveProbeSource `json:"source,omitempty"`
	SourceStatus   string            `json:"source_status,omitempty"`
}

type serveProbeSource struct {
	File            string `json:"file"`
	SHA256          string `json:"sha256"`
	Bytes           int    `json:"bytes"`
	MatchesResponse *bool  `json:"matches_response,omitempty"`
}

func (t *Serve) probe(ctx context.Context, port int, target, method string) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("probe needs an owned port between 1 and 65535 (see serve action=list)")
	}
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return "", fmt.Errorf("probe method must be GET or HEAD")
	}
	if target == "" {
		target = "/"
	}
	u, err := url.Parse(target)
	if err != nil || len(target) > 1024 || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(target, "#") || u.Opaque != "" || strings.HasPrefix(u.Path, "//") || strings.Contains(u.Path, "\\") || strings.IndexFunc(u.Path, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("probe path must be a local URL path, at most 1024 bytes, without a host, query, fragment or control characters")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == ".." || segment == "." {
			return "", fmt.Errorf("probe path must not contain traversal segments")
		}
	}
	u.Path = "/" + strings.TrimPrefix(u.Path, "/")
	u.RawPath = ""
	t.mu.Lock()
	owned := t.srv[port]
	t.mu.Unlock()
	if owned == nil {
		return "", fmt.Errorf("probe refuses unowned port %d; start this workspace's server or use serve action=list", port)
	}
	if !owned.mu.TryRLock() {
		return "", fmt.Errorf("owned server on port %d is stopping; probe not started", port)
	}
	defer owned.mu.RUnlock()
	if owned.closed {
		return "", fmt.Errorf("owned server on port %d is unavailable", port)
	}
	report := serveProbeResult{Schema: "cerveau.serve-probe.v1", Outcome: "unverified", ServerID: owned.id, Origin: owned.origin, Root: owned.root, Path: u.EscapedPath(), Method: method}
	finish := func(err error) (string, error) {
		raw, _ := json.Marshal(report)
		return string(raw), err
	}
	probeCtx, cancel := context.WithTimeout(ctx, serveProbeTimeout)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 8192,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(probeCtx, method, owned.origin+u.EscapedPath(), nil)
	if err != nil {
		return finish(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return finish(fmt.Errorf("owned server probe failed: %w", err))
	}
	defer resp.Body.Close()
	report.Status, report.MIME = resp.StatusCode, resp.Header.Get("Content-Type")
	if resp.Header.Get("X-Cerveau-Serve-ID") != owned.id {
		return finish(fmt.Errorf("probe response does not match the owned server identity"))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, serveProbeLimit+1))
	report.Bytes = len(body)
	if err != nil {
		return finish(fmt.Errorf("probe response incomplete: %w", err))
	}
	if len(body) > serveProbeLimit {
		return finish(fmt.Errorf("probe response exceeds %d bytes; observation incomplete", serveProbeLimit))
	}
	report.BodyComplete = true
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return finish(fmt.Errorf("owned server returned HTTP %d; redirects are not followed", resp.StatusCode))
	}
	if method == http.MethodGet {
		report.ResponseSHA256 = fmt.Sprintf("%x", sha256.Sum256(body))
	}
	report.Source, report.SourceStatus = owned.probeSource(u.Path, method, report.ResponseSHA256)
	report.Outcome = "observed"
	return finish(nil)
}

func (s *servedStatic) probeSource(target, method, responseSHA string) (*serveProbeSource, string) {
	if !s.rootCurrent() {
		return nil, "served root identity changed"
	}
	name := strings.TrimPrefix(target, "/")
	if name == "" || strings.HasSuffix(name, "/") {
		name += "index.html"
	}
	info, err := s.files.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > serveProbeLimit {
		return nil, "no bounded regular source file"
	}
	f, err := s.files.Open(name)
	if err != nil {
		return nil, "no bounded regular source file"
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > serveProbeLimit || !os.SameFile(info, before) {
		return nil, "no bounded regular source file"
	}
	data, err := io.ReadAll(io.LimitReader(f, serveProbeLimit+1))
	if err != nil || len(data) > serveProbeLimit {
		return nil, "source read incomplete"
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(data)) != after.Size() {
		return nil, "source changed during observation"
	}
	source := &serveProbeSource{File: path.Join(s.root, name), Bytes: len(data), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	if method == http.MethodGet {
		matches := source.SHA256 == responseSHA
		source.MatchesResponse = &matches
		if !matches {
			return source, "source differs from HTTP response"
		}
	}
	return source, "observed separately; delivery is not an application check"
}

// A retained directory handle cannot escape via symlinks, but an operator can
// rename/replace the directory itself. Do not label bytes from that old handle
// as belonging to the replacement workspace path.
func (s *servedStatic) rootCurrent() bool {
	full, err := (jail{root: s.workspace}).resolve(filepath.FromSlash(s.root))
	if err != nil {
		return false
	}
	current, err := os.Stat(full)
	return err == nil && os.SameFile(s.info, current)
}

// Static serving never needs named pipes, sockets or devices. Inspect the
// confined target before opening so an ordinary FIFO cannot hang a handler.
// os.Root still enforces confinement at the final open, including symlinks.
type serveRootFS struct{ root *os.Root }

func (s serveRootFS) Open(name string) (fs.File, error) {
	info, err := s.root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, fs.ErrPermission
	}
	return s.root.Open(name)
}

func dirLabel(dir string) string {
	if dir == "" {
		return "the workspace"
	}
	return dir
}
