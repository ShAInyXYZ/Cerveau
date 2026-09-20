package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func applyCore(t *testing.T, a *API, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	a.ApplyCore(rec, httptest.NewRequest(http.MethodPost, "/api/cores/apply", strings.NewReader(body)))
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A switch stops the Core and restarts Cerveau. A run the user is not watching
// must not be lost to a button press: refuse, say which sessions, and write
// NOTHING — not the active Core, not the switch request.
func TestApplyCoreRefusesWhileARunIsInProgress(t *testing.T) {
	a := paramsFixture(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	a.runningFn = func() []string { return []string{"sess_a", "sess_b"} }
	before, _ := os.ReadFile(os.Getenv("CRV_CORES_JSON"))

	code, out := applyCore(t, a, `{"id":"bf16"}`)
	if code != http.StatusConflict {
		t.Fatalf("want 409, got %d %v", code, out)
	}
	if got, _ := out["running"].([]any); len(got) != 2 {
		t.Fatalf("the refusal must name the sessions: %v", out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "2 runs in progress") {
		t.Fatalf("error = %q", msg)
	}
	after, _ := os.ReadFile(os.Getenv("CRV_CORES_JSON"))
	if string(before) != string(after) {
		t.Fatal("a refused apply must not touch cores.json")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "cerveau", "switch-request.json")); err == nil {
		t.Fatal("a refused apply must not queue a switch")
	}
}

// Told which runs it interrupts, the user may still ask for it.
func TestApplyCoreForcedGoesThrough(t *testing.T) {
	a := paramsFixture(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	a.runningFn = func() []string { return []string{"sess_a"} }
	if code, out := applyCore(t, a, `{"id":"bf16","force":true}`); code != http.StatusOK {
		t.Fatalf("want 200, got %d %v", code, out)
	}
}

func TestApplyCoreWithNothingRunning(t *testing.T) {
	a := paramsFixture(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	a.runningFn = func() []string { return nil }
	if code, out := applyCore(t, a, `{"id":"bf16"}`); code != http.StatusOK {
		t.Fatalf("want 200, got %d %v", code, out)
	}
}
