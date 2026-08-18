package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAskCreatesSessionAndChats(t *testing.T) {
	var chatBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/sessions":
			json.NewEncoder(w).Encode(map[string]any{"id": "sess_1", "name": "hi"})
		case r.Method == "POST" && r.URL.Path == "/api/sessions/sess_1/chat":
			json.NewDecoder(r.Body).Decode(&chatBody)
			json.NewEncoder(w).Encode(map[string]any{"reply": "hello back", "iterations": 1, "capped": false})
		default:
			http.Error(w, `{"error":"not found"}`, 404)
		}
	}))
	defer srv.Close()

	c := &client{base: srv.URL}
	if err := c.ask("", "discussion", t.TempDir(), []string{"say", "hi"}); err != nil {
		t.Fatal(err)
	}
	if chatBody["text"] != "say hi" {
		t.Fatalf("chat text = %q", chatBody["text"])
	}
	if chatBody["mode"] != "discussion" {
		t.Fatalf("mode = %q", chatBody["mode"])
	}
}

func TestAskExistingSessionSkipsCreate(t *testing.T) {
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/api/sessions" && r.Method == "POST" {
			created = true
		}
		if strings.HasSuffix(r.URL.Path, "/chat") {
			json.NewEncoder(w).Encode(map[string]any{"reply": "ok"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := &client{base: srv.URL}
	if err := c.ask("sess_existing", "", "", []string{"go"}); err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("should not create a session when -session is given")
	}
}

func TestErrorResponseSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(502)
		w.Write([]byte(`{"error":"loop not wired"}`))
	}))
	defer srv.Close()

	c := &client{base: srv.URL}
	err := c.ask("s1", "", "", []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "loop not wired") {
		t.Fatalf("expected surfaced error, got %v", err)
	}
}

func TestUnreachableServer(t *testing.T) {
	c := &client{base: "http://127.0.0.1:1"} // nothing listening
	err := c.health()
	// A refused connection and a turn that outran the timeout are DIFFERENT
	// failures and must read differently — "is the server running?" on a
	// timeout sends the user to check a healthy service.
	if err == nil || !strings.Contains(err.Error(), "could not connect") {
		t.Fatalf("expected a connection error, got %v", err)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("hello world\nsecond", 40); got != "hello world" {
		t.Fatalf("firstLine = %q", got)
	}
	if got := firstLine(strings.Repeat("x", 100), 10); len(got) != 10 {
		t.Fatalf("firstLine not capped: %d", len(got))
	}
}
