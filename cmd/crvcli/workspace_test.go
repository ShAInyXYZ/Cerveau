package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The CLI is INDEPENDENT of the WebUI.
//
// The workspace belongs to the project: every session in it shares that path.
// The panel sets it with a folder picker, which writes the core's GLOBAL
// workspace. A CLI caller has no picker — so it must be able to state the
// workspace itself, and the WebUI must never redirect work already running
// from the terminal.
//
// Before this, `crvcli ask` created a session with only a name, so it silently
// inherited whatever folder the panel last pointed at. Running a benchmark in
// ~/Pictures/Benchmark had its tools act on an unrelated chess project.

// captureCreate returns the body the CLI POSTs to /api/sessions.
func captureCreate(t *testing.T, run func(c *client) error) map[string]any {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sessions" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = w.Write([]byte(`{"id":"s1","name":"n","workspace":"` + str(got["workspace"]) + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"reply":"ok"}`))
	}))
	defer srv.Close()
	if err := run(&client{base: srv.URL}); err != nil {
		t.Fatalf("command failed: %v", err)
	}
	return got
}

func TestAskSendsExplicitWorkspace(t *testing.T) {
	dir := t.TempDir()
	got := captureCreate(t, func(c *client) error {
		return c.ask("", "", dir, []string{"do the thing"})
	})
	if got["workspace"] != dir {
		t.Errorf("workspace sent = %q, want %q — a CLI session must not\n"+
			"inherit whatever folder the WebUI picker last set", got["workspace"], dir)
	}
}

func TestNewSessionSendsExplicitWorkspace(t *testing.T) {
	dir := t.TempDir()
	got := captureCreate(t, func(c *client) error {
		return c.newSession(dir, []string{"my", "task"})
	})
	if got["workspace"] != dir {
		t.Errorf("workspace sent = %q, want %q", got["workspace"], dir)
	}
}

// No -workspace given: the CLI still pins the session to where the user
// actually is (cwd), rather than leaving it to the panel's global setting.
func TestWorkspaceDefaultsToCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	got := captureCreate(t, func(c *client) error {
		return c.ask("", "", "", []string{"where am i"})
	})
	ws := str(got["workspace"])
	if ws == "" {
		t.Fatal("no workspace sent — the session would inherit the WebUI's global folder")
	}
	// macOS/TMPDIR can symlink; compare resolved paths
	want, _ := os.Getwd()
	if resolve(ws) != resolve(want) {
		t.Errorf("workspace = %q, want cwd %q", ws, want)
	}
}

// A relative -workspace must be resolved before it leaves the client: the
// server has a different working directory, so "." would mean the wrong place.
func TestRelativeWorkspaceIsResolvedToAbsolute(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	got := captureCreate(t, func(c *client) error {
		return c.ask("", "", ".", []string{"here"})
	})
	ws := str(got["workspace"])
	if !strings.HasPrefix(ws, "/") {
		t.Errorf("workspace %q is not absolute — the server resolves paths from ITS cwd", ws)
	}
}

// A workspace that does not exist must fail LOUDLY at the client, not create a
// session that quietly acts somewhere else.
func TestMissingWorkspaceIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"s1"}`))
	}))
	defer srv.Close()
	c := &client{base: srv.URL}
	err := c.ask("", "", "/definitely/not/a/real/dir/xyz", []string{"go"})
	if err == nil {
		t.Fatal("expected an error for a nonexistent workspace")
	}
	if !strings.Contains(err.Error(), "workspace") {
		t.Errorf("error should name the workspace, got: %v", err)
	}
}

func resolve(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

// A turn that outruns the client timeout must NOT be reported as an
// unreachable server: the server is fine and the turn may still be running.
func TestTimeoutIsNotReportedAsServerDown(t *testing.T) {
	t.Setenv("CRV_TIMEOUT", "50ms")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond) // slower than the client's patience
	}))
	defer srv.Close()

	err := (&client{base: srv.URL}).health()
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	msg := err.Error()
	if strings.Contains(msg, "is the server running") || strings.Contains(msg, "could not connect") {
		t.Errorf("timeout reported as a dead server: %q", msg)
	}
	if !strings.Contains(msg, "CRV_TIMEOUT") {
		t.Errorf("message should name the knob that fixes it: %q", msg)
	}
}

func TestCRVTimeoutIsHonoured(t *testing.T) {
	t.Setenv("CRV_TIMEOUT", "42m")
	if got := clientTimeout(); got != 42*time.Minute {
		t.Errorf("clientTimeout() = %v, want 42m", got)
	}
}
