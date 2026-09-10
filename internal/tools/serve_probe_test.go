package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func startProbeServer(t *testing.T, workspace, dir string) (*Serve, int) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	server := NewServe(workspace)
	t.Cleanup(server.stopAll)
	if _, err := server.start(port, dir); err != nil {
		t.Fatal(err)
	}
	return server, port
}

func serveProbeCall(t *testing.T, server *Serve, port int, target, method string) (serveProbeResult, string, error) {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"action": "probe", "port": port, "path": target, "method": method})
	out, err := server.Execute(context.Background(), args)
	var report serveProbeResult
	if out != "" {
		if decodeErr := json.Unmarshal([]byte(out), &report); decodeErr != nil {
			t.Fatalf("probe result was not structured JSON: %q %v", out, decodeErr)
		}
	}
	return report, out, err
}

func TestServeRootIdentityRejectsWrongDirectory(t *testing.T) {
	workspace := t.TempDir()
	for _, dir := range []string{"first", "second"} {
		if err := os.Mkdir(filepath.Join(workspace, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	server, port := startProbeServer(t, workspace, "first")
	before := server.list()
	if !strings.Contains(before, `root "first"`) || !strings.Contains(before, "server serve_") {
		t.Fatal("list lost exact root/identity", before)
	}
	if out, err := server.start(port, "second"); err == nil || !strings.Contains(err.Error(), `root "first"`) || !strings.Contains(err.Error(), `root "second"`) {
		t.Fatalf("different root silently reused old server: %q %v", out, err)
	}
	if after := server.list(); before != after {
		t.Fatalf("rejected start changed owned server: %q -> %q", before, after)
	}
	if out, err := server.start(port, "./first"); err != nil || !strings.Contains(out, "already") {
		t.Fatalf("equivalent root was not idempotent: %q %v", out, err)
	}
	// Directory replacement at the same path is a different served identity;
	// the running handle still points at the original directory.
	if err := os.Rename(filepath.Join(workspace, "first"), filepath.Join(workspace, "old-first")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "first"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := server.start(port, "first"); err == nil {
		t.Fatal("replacement directory was confused with captured server root")
	}
	if report, _, err := serveProbeCall(t, server, port, "/", "GET"); err == nil || report.Status != http.StatusConflict || report.Source != nil {
		t.Fatalf("old root bytes mislabeled as replacement directory: %+v %v", report, err)
	}
}

func TestServeProbeReportsHTTPAndBoundedSourceIdentity(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte("<!doctype html><title>local fixture</title><p>body is not tool output</p>")
	if err := os.WriteFile(filepath.Join(workspace, "dist", "index.html"), body, 0600); err != nil {
		t.Fatal(err)
	}
	server, port := startProbeServer(t, workspace, "dist")
	report, out, err := serveProbeCall(t, server, port, "/", "GET")
	if err != nil {
		t.Fatal(out, err)
	}
	wantSHA := fmt.Sprintf("%x", sha256.Sum256(body))
	if report.Outcome != "observed" || report.Status != 200 || !strings.HasPrefix(report.MIME, "text/html") || report.Bytes != len(body) || !report.BodyComplete {
		t.Fatalf("wrong HTTP observation: %+v", report)
	}
	if report.ServerID == "" || report.Root != "dist" || report.Origin != fmt.Sprintf("http://127.0.0.1:%d", port) || report.ResponseSHA256 != wantSHA {
		t.Fatalf("wrong server/response identity: %+v", report)
	}
	if report.Source == nil || report.Source.File != "dist/index.html" || report.Source.SHA256 != wantSHA || report.Source.MatchesResponse == nil || !*report.Source.MatchesResponse {
		t.Fatalf("source did not match delivered bytes: %+v", report)
	}
	if strings.Contains(out, "body is not tool output") || len(out) > 1000 {
		t.Fatalf("probe emitted source or exceeded ordinary ingress budget: %d %s", len(out), out)
	}
	args, _ := json.Marshal(map[string]any{"action": "probe", "port": port})
	if _, err := server.Execute(WithRecoveryShell(context.Background()), args); err != nil {
		t.Fatalf("native owned-server probe unavailable in recovery: %v", err)
	}
	head, _, err := serveProbeCall(t, server, port, "/", "HEAD")
	if err != nil || head.Status != 200 || head.Bytes != 0 || head.Source == nil || head.Source.SHA256 != wantSHA || head.Source.MatchesResponse != nil || head.ResponseSHA256 != "" {
		t.Fatalf("HEAD claimed an unobserved response body: %+v %v", head, err)
	}
}

func TestServeProbeRejectsUnownedUnavailableAndMissingTargets(t *testing.T) {
	var unownedRequests atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unownedRequests.Add(1)
		w.Write([]byte("must not contact this local service"))
	}))
	defer foreign.Close()
	u, _ := url.Parse(foreign.URL)
	foreignPort, _ := strconv.Atoi(u.Port())
	server := NewServe(t.TempDir())
	if _, _, err := serveProbeCall(t, server, foreignPort, "/", "GET"); err == nil || unownedRequests.Load() != 0 {
		t.Fatalf("probe contacted an unowned local server: requests=%d err=%v", unownedRequests.Load(), err)
	}
	owned, port := startProbeServer(t, t.TempDir(), "")
	report, _, err := serveProbeCall(t, owned, port, "/missing.html", "GET")
	if err == nil || report.Status != 404 || report.Outcome != "unverified" || report.Source != nil {
		t.Fatalf("missing HTTP route passed: %+v %v", report, err)
	}
	owned.mu.Lock()
	entry := owned.srv[port]
	owned.mu.Unlock()
	entry.server.Close() // simulate an owned listener stopping unexpectedly
	if report, _, err := serveProbeCall(t, owned, port, "/", "GET"); err == nil || report.Outcome == "observed" {
		t.Fatalf("unavailable owned server passed: %+v %v", report, err)
	}
}

func TestServeProbeBoundsResponsesAndDoesNotFollowRedirects(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte("page"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "large.txt"), []byte(strings.Repeat("x", serveProbeLimit+20)), 0600); err != nil {
		t.Fatal(err)
	}
	server, port := startProbeServer(t, workspace, "")
	redirect, _, err := serveProbeCall(t, server, port, "/index.html", "GET")
	if err == nil || redirect.Status != http.StatusMovedPermanently || redirect.Outcome != "unverified" {
		t.Fatalf("probe followed redirect or called it success: %+v %v", redirect, err)
	}
	large, out, err := serveProbeCall(t, server, port, "/large.txt", "GET")
	if err == nil || large.Bytes != serveProbeLimit+1 || large.BodyComplete || large.Outcome != "unverified" || large.Source != nil || len(out) > 1000 {
		t.Fatalf("oversized response was not bounded: %+v %v", large, err)
	}
	head, _, err := serveProbeCall(t, server, port, "/large.txt", "HEAD")
	if err != nil || head.Bytes != 0 || head.Source != nil || head.SourceStatus == "" {
		t.Fatalf("HEAD hashed an oversized source: %+v %v", head, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := server.probe(cancelled, port, "/", "GET"); err == nil {
		t.Fatal("probe ignored caller cancellation")
	}
}

func TestServeProbeRejectsURLsTraversalAndMutatingMethods(t *testing.T) {
	server, port := startProbeServer(t, t.TempDir(), "")
	for _, target := range []string{"http://127.0.0.1:7700/", "//localhost:7700/", "https://example.com/", "/../outside", "/%2e%2e/outside", "/%2f%2flocalhost:7700/", "/a\\b", "/a?query=x", "/#fragment", "/#", "/%00", strings.Repeat("a", 1025)} {
		t.Run(target, func(t *testing.T) {
			if _, _, err := serveProbeCall(t, server, port, target, "GET"); err == nil {
				t.Fatalf("out-of-scope probe path accepted: %q", target)
			}
		})
	}
	for _, method := range []string{"POST", "DELETE", "PUT", "get"} {
		if _, _, err := serveProbeCall(t, server, port, "/", method); err == nil {
			t.Fatalf("mutating/unsupported method %s accepted", method)
		}
	}
}

func TestServeProbeRejectsSpecialFilesWithoutBlocking(t *testing.T) {
	workspace := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(workspace, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	server, port := startProbeServer(t, workspace, "")
	if report, _, err := serveProbeCall(t, server, port, "/pipe", "GET"); err == nil || report.Status != http.StatusForbidden || report.Source != nil {
		t.Fatalf("non-static file was not refused: %+v %v", report, err)
	}
}

func TestServeConfinesSymlinksOnEveryRequest(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "public"), 0700); err != nil {
		t.Fatal(err)
	}
	const secret = "must remain outside served directory"
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "private.txt"), []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "public", "safe.txt"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	server, port := startProbeServer(t, workspace, "public")
	links := map[string]string{
		"external.txt":  filepath.Join(outside, "secret.txt"),
		"external":      outside,
		"sibling.txt":   "../private.txt",
		"safe-link.txt": "safe.txt",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(workspace, "public", name)); err != nil {
			t.Fatal(err)
		}
	}
	// Links are introduced AFTER start, so one-time root validation cannot
	// make this test pass. Both the HTTP serving path and probe must reject.
	for _, target := range []string{"external.txt", "external/secret.txt", "sibling.txt"} {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/%s", port, target))
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || resp.StatusCode >= 200 && resp.StatusCode < 300 || strings.Contains(string(body), secret) {
			t.Fatalf("serve escaped through %s: status=%d body=%q err=%v", target, resp.StatusCode, body, readErr)
		}
		if report, _, err := serveProbeCall(t, server, port, target, "GET"); err == nil || report.Source != nil {
			t.Fatalf("probe escaped through %s: %+v %v", target, report, err)
		}
	}
	if safe, _, err := serveProbeCall(t, server, port, "safe-link.txt", "GET"); err != nil || safe.Source == nil || safe.Bytes != 4 {
		t.Fatalf("safe relative symlink was not readable: %+v %v", safe, err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "outside-root")); err != nil {
		t.Fatal(err)
	}
	if _, err := server.start(port+1, "outside-root"); err == nil {
		t.Fatal("outside symlink accepted as a served root")
	}
}
