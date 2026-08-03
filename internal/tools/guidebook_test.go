package tools

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

// The guidebook turns trivially-fixable tool failures into automatic repairs:
// the harness fixes the args and retries instead of surfacing an error card.
// Rule 1: serve port-in-use → try the next port.
func TestGuidebookServePortBump(t *testing.T) {
	newArgs, note, ok := guidebookRepair("serve",
		json.RawMessage(`{"action":"start","dir":"chess","port":8000}`),
		"cannot bind port 8000: listen tcp 127.0.0.1:8000: bind: address already in use — try another port")
	if !ok {
		t.Fatal("port-in-use should be repairable")
	}
	var a struct {
		Port int `json:"port"`
	}
	json.Unmarshal(newArgs, &a)
	if a.Port != 8001 {
		t.Errorf("port should bump to 8001, got %d", a.Port)
	}
	if !strings.Contains(note, "8000") || !strings.Contains(note, "8001") {
		t.Errorf("note should name both ports: %q", note)
	}
}

// Rule 2: grep "bad regex" → retry the pattern as a literal string.
func TestGuidebookGrepLiteral(t *testing.T) {
	newArgs, note, ok := guidebookRepair("grep",
		json.RawMessage(`{"pattern":"foo(bar"}`),
		"bad regex: error parsing regexp: missing closing )")
	if !ok {
		t.Fatal("bad regex should be repairable")
	}
	var a struct {
		Pattern string `json:"pattern"`
	}
	json.Unmarshal(newArgs, &a)
	if a.Pattern != `foo\(bar` {
		t.Errorf("pattern should be quoted literal, got %q", a.Pattern)
	}
	if !strings.Contains(note, "literal") {
		t.Errorf("note should say it retried as literal: %q", note)
	}
}

// Unmatched failures must NOT be repaired — the model needs to see real errors.
func TestGuidebookLeavesRealErrorsAlone(t *testing.T) {
	if _, _, ok := guidebookRepair("edit", json.RawMessage(`{}`), "old_string not found in x.js"); ok {
		t.Error("edit errors are not auto-fixable")
	}
	if _, _, ok := guidebookRepair("serve", json.RawMessage(`{"action":"stop"}`), "stop needs a port"); ok {
		t.Error("non-bind serve errors are not auto-fixable")
	}
}

// End to end through the registry: a serve start on an occupied port must
// succeed automatically on the next port, with the auto-fix noted in the output.
func TestRegistryAutoFixesOccupiedPort(t *testing.T) {
	// occupy a port for real
	ln, err := net.Listen("tcp", "127.0.0.1:8971")
	if err != nil {
		t.Skip("cannot occupy test port")
	}
	defer ln.Close()

	dir := t.TempDir()
	srv := NewServe(dir)
	defer srv.stopAll()
	reg := NewRegistry(Entry{Tool: srv, RiskTier: RiskSafe})

	out, err := reg.Execute(context.Background(), "serve",
		json.RawMessage(`{"action":"start","port":8971}`))
	if err != nil {
		t.Fatalf("should auto-fix to the next port, got error: %v", err)
	}
	if !strings.Contains(out, "8972") {
		t.Errorf("output should show the working port 8972: %q", out)
	}
	if !strings.Contains(out, "auto-fixed") {
		t.Errorf("output should disclose the auto-fix: %q", out)
	}
}
