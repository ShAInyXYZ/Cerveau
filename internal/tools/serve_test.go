package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serve must start a static server that OUTLIVES the tool call (unlike bash,
// which kills its process group), return the URL immediately, and serve files
// from the workspace. stop must kill it.
func TestServeStartServesAndStop(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi from serve</h1>"), 0o644)
	s := NewServe(dir)
	defer s.stopAll()

	// pick a high port unlikely to clash
	out, err := s.Execute(context.Background(), json.RawMessage(`{"action":"start","port":8971}`))
	if err != nil {
		t.Fatalf("start failed: %v (%s)", err, out)
	}
	if !strings.Contains(out, "8971") || !strings.Contains(out, "http://") {
		t.Fatalf("start should return the URL: %q", out)
	}

	// the server must actually respond — and keep responding after Execute returned
	time.Sleep(150 * time.Millisecond)
	resp, err := http.Get("http://127.0.0.1:8971/index.html")
	if err != nil {
		t.Fatalf("server not reachable after the tool call returned: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "hi from serve") {
		t.Fatalf("wrong body: %q", body)
	}

	// list shows it
	list, _ := s.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if !strings.Contains(list, "8971") {
		t.Fatalf("list should show the running server: %q", list)
	}

	// stop kills it
	if _, err := s.Execute(context.Background(), json.RawMessage(`{"action":"stop","port":8971}`)); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := http.Get("http://127.0.0.1:8971/index.html"); err == nil {
		t.Fatal("server still reachable after stop")
	}
}

// Starting twice on the same port should reuse, not error or leak.
func TestServeIdempotentStart(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644)
	s := NewServe(dir)
	defer s.stopAll()

	if _, err := s.Execute(context.Background(), json.RawMessage(`{"action":"start","port":8972}`)); err != nil {
		t.Fatal(err)
	}
	out, err := s.Execute(context.Background(), json.RawMessage(`{"action":"start","port":8972}`))
	if err != nil {
		t.Fatalf("second start should reuse, not fail: %v (%s)", err, out)
	}
	if !strings.Contains(out, "already") {
		t.Fatalf("second start should say it's already running: %q", out)
	}
}
