package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	webFetchTimeout  = 20 * time.Second
	webFetchMaxBody  = 2 << 20
	webFetchCapChars = 8000
)

type WebFetch struct {
	http *http.Client
}

func NewWebFetch() *WebFetch {
	return &WebFetch{http: &http.Client{Timeout: webFetchTimeout}}
}

func (t *WebFetch) Name() string { return "web_fetch" }

func (t *WebFetch) Description() string {
	return "Fetch a URL and return its text content (HTML stripped). For research in Brainstorming mode."
}

func (t *WebFetch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string"},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.URL == "" {
		return "", fmt.Errorf("url required")
	}
	if !strings.HasPrefix(a.URL, "http://") && !strings.HasPrefix(a.URL, "https://") {
		return "", fmt.Errorf("url must start with http:// or https://")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("user-agent", "cerveau/1.0 (+local research agent)")
	resp, err := t.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBody))
	if err != nil {
		return "", err
	}
	text := htmlToText(string(body))
	if len(text) > webFetchCapChars {
		text = text[:webFetchCapChars] + fmt.Sprintf("\n...[truncated, %d chars total]", len(text))
	}
	return text, nil
}

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reTags        = regexp.MustCompile(`(?s)<[^>]+>`)
	reWs          = regexp.MustCompile(`\s+`)
)

func htmlToText(s string) string {
	s = reScriptStyle.ReplaceAllString(s, " ")
	s = reTags.ReplaceAllString(s, " ")
	replacer := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
	)
	s = replacer.Replace(s)
	return strings.TrimSpace(reWs.ReplaceAllString(s, " "))
}
