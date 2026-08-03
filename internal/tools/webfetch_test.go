package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fetchArgs(t *testing.T, w *WebFetch, args string) (string, error) {
	t.Helper()
	return w.Execute(context.Background(), json.RawMessage(args))
}

// A documentation-style page: the article must survive as markdown WITH its
// code block fenced and its table intact; nav/footer boilerplate must be gone.
func docPage() string {
	return `<!DOCTYPE html><html><head><title>API Guide</title></head><body>
	<nav><ul><li><a href="/">Home</a></li><li><a href="/about">About</a></li></ul></nav>
	<article>
	<h1>API Guide</h1>
	<p>Call the endpoint with a payload. This paragraph gives the article enough
	body text that the readability scorer treats it as real content worth keeping,
	rather than discarding the whole page as boilerplate.</p>
	<pre><code>curl -X POST /api/run -d '{"x": 1}'</code></pre>
	<h2>Parameters</h2>
	<p>The endpoint accepts the following parameters, described in the table.</p>
	<table><tr><th>name</th><th>type</th></tr><tr><td>x</td><td>int</td></tr></table>
	</article>
	<footer>Copyright 2026 · Terms · Privacy · Cookie settings</footer>
	</body></html>`
}

func TestWebFetchExtractsCleanMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, docPage())
	}))
	defer srv.Close()

	out, err := fetchArgs(t, NewWebFetch(), `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(out, "curl -X POST") {
		t.Errorf("code block content lost: %q", out)
	}
	if !strings.Contains(out, "```") {
		t.Errorf("code block should be fenced markdown: %q", out)
	}
	if !strings.Contains(out, "| name") && !strings.Contains(out, "| x") {
		t.Errorf("table lost: %q", out)
	}
	if strings.Contains(out, "Cookie settings") {
		t.Errorf("footer boilerplate survived: %q", out)
	}
}

// The tool must identify itself honestly — never impersonate a browser.
func TestWebFetchHonestUserAgent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		fmt.Fprint(w, docPage())
	}))
	defer srv.Close()
	fetchArgs(t, NewWebFetch(), `{"url":"`+srv.URL+`"}`)
	if !strings.Contains(ua, "Cerveau") {
		t.Errorf("UA must identify Cerveau honestly, got %q", ua)
	}
	low := strings.ToLower(ua)
	if strings.Contains(low, "mozilla") || strings.Contains(low, "chrome") {
		t.Errorf("UA must not impersonate a browser: %q", ua)
	}
}

// Big pages must come back as an OUTLINE (headings + sizes), not a truncated
// dump — the 32K-window consumer drills into a section by name.
func TestWebFetchOutlineAndSection(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><head><title>Big Spec</title></head><body><article><h1>Big Spec</h1>`)
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&b, "<h2>Chapter %d</h2>", i)
		for j := 0; j < 120; j++ {
			fmt.Fprintf(&b, "<p>Chapter %d filler sentence number %d with some length to it.</p>", i, j)
		}
	}
	b.WriteString(`</article></body></html>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, b.String())
	}))
	defer srv.Close()

	w := NewWebFetch()
	out, err := fetchArgs(t, w, `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(out, "outline") || !strings.Contains(out, "Chapter 3") {
		t.Errorf("big page should return an outline naming its sections: %.400q", out)
	}
	if strings.Count(out, "filler sentence") > 5 {
		t.Errorf("outline should not dump section bodies")
	}

	sec, err := fetchArgs(t, w, `{"url":"`+srv.URL+`","section":"Chapter 3"}`)
	if err != nil {
		t.Fatalf("section fetch: %v", err)
	}
	if !strings.Contains(sec, "Chapter 3 filler sentence number 1") {
		t.Errorf("section body missing: %.200q", sec)
	}
	if strings.Contains(sec, "Chapter 4 filler") {
		t.Errorf("section fetch leaked the next chapter")
	}
}

// start_index continues linear reading past the beginning.
func TestWebFetchStartIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, docPage())
	}))
	defer srv.Close()
	out, err := fetchArgs(t, NewWebFetch(), `{"url":"`+srv.URL+`","start_index":30}`)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if strings.Contains(out, "# API Guide") {
		t.Errorf("start_index should skip the beginning: %.200q", out)
	}
}

// Failures must be explained in plain language, never raw HTML or bare codes.
func TestWebFetchFriendlyErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gone":
			http.NotFound(w, r)
		case "/challenge":
			w.WriteHeader(403)
			fmt.Fprint(w, `<html><head><title>Just a moment...</title></head><body>Checking your browser. cf-chl-widget</body></html>`)
		}
	}))
	defer srv.Close()

	// A 404 is INFORMATION, not a malfunction: it must come back as a normal
	// result so exploratory URL misses never consume the guard's error budget
	// (three guessed-wrong Wikipedia URLs killed a whole research turn).
	out, err := fetchArgs(t, NewWebFetch(), `{"url":"`+srv.URL+`/gone"}`)
	if err != nil {
		t.Errorf("404 must be a result, not an error (guard budget): %v", err)
	}
	if !strings.Contains(out, "404") || !strings.Contains(out, "not exist") {
		t.Errorf("404 result should say the page does not exist: %q", out)
	}

	out, err = fetchArgs(t, NewWebFetch(), `{"url":"`+srv.URL+`/challenge"}`)
	if err != nil {
		t.Errorf("bot challenge must be a result, not an error: %v", err)
	}
	if !strings.Contains(out, "blocks automated access") {
		t.Errorf("bot challenge should be identified plainly: %q", out)
	}
}

// A page with no article structure must still return its content (the
// empty-extraction fallback), never an empty string.
func TestWebFetchEmptyExtractionFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><div>just a bare div with the answer: 42</div></body></html>`)
	}))
	defer srv.Close()
	out, err := fetchArgs(t, NewWebFetch(), `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("fallback should return the page content: %q", out)
	}
}
