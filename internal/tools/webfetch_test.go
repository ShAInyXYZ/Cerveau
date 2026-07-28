package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchStripsHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html")
		w.Write([]byte(`<html><head><style>body{color:red}</style><script>var x=1;</script></head>
		<body><h1>Hello   World</h1><p>Some &amp; text</p></body></html>`))
	}))
	defer srv.Close()
	wf := NewWebFetch()
	out, err := wf.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "var x=1") || strings.Contains(out, "color:red") || strings.Contains(out, "<") {
		t.Fatalf("html not stripped: %q", out)
	}
	if !strings.Contains(out, "Hello World") || !strings.Contains(out, "Some & text") {
		t.Fatalf("content lost: %q", out)
	}
}

func TestWebFetchBadURL(t *testing.T) {
	wf := NewWebFetch()
	if _, err := wf.Execute(context.Background(), json.RawMessage(`{"url":"ftp://x"}`)); err == nil {
		t.Fatal("expected scheme error")
	}
	if _, err := wf.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected url required error")
	}
}

func TestWebFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	wf := NewWebFetch()
	if _, err := wf.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`)); err == nil {
		t.Fatal("expected http error")
	}
}

func TestBrainstormingModeRegistered(t *testing.T) {
	reg := NewRegistry(Entry{Tool: NewWebFetch(), RiskTier: RiskSafe, Modes: []string{ModeBrainstorming}})
	specs := reg.Specs(ModeBrainstorming)
	if len(specs) != 1 {
		t.Fatalf("brainstorming specs = %d", len(specs))
	}
	if len(reg.Specs(ModeDiscussion)) != 0 {
		t.Fatal("web_fetch leaked into discussion")
	}
}
