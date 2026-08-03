package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	readability "github.com/go-shiori/go-readability"
	"golang.org/x/net/html/charset"
	nurl "net/url"
)

// web_fetch turns a URL into clean, small-window-sized markdown. The pipeline
// is the industry-standard single-page core (what Jina Reader and Firecrawl
// run behind their APIs), in-process with zero services:
//
//	honest HTTP GET → charset transcode → Readability (main content)
//	→ html-to-markdown (code blocks + tables preserved)
//	→ outline/section/offset budgeting for the 32K-context consumer
//	→ lazy headless-Chromium render ONLY when the page demonstrably needs JS
//
// Legitimacy by design, not evasion: the User-Agent names Cerveau truthfully,
// no browser impersonation, no TLS fingerprint spoofing, and a site that
// blocks automated access gets reported as exactly that — we do not fight
// bot protection, we say so and stop.
const (
	webFetchTimeout  = 20 * time.Second
	webFetchMaxBody  = 4 << 20
	webFetchCapChars = 8000 // per-call output budget (~2K tokens)
	// A descriptive, honest UA. Never a browser string: spoofing is what bot
	// detection punishes; honesty plus low volume is what gets tolerated.
	webFetchUA = "Cerveau/0.3 (local coding agent; +https://cerveau.sh)"
)

type WebFetch struct {
	http *http.Client
}

func NewWebFetch() *WebFetch {
	return &WebFetch{http: &http.Client{Timeout: webFetchTimeout}}
}

func (t *WebFetch) Name() string { return "web_fetch" }

func (t *WebFetch) Description() string {
	return "Fetch a URL as clean markdown (main content only; code blocks and tables preserved). " +
		"Large pages return an OUTLINE of sections — call again with section=\"<heading>\" to read one, " +
		"or start_index=<n> to continue reading from an offset."
}

func (t *WebFetch) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":         map[string]any{"type": "string"},
			"section":     map[string]any{"type": "string", "description": "heading name from the outline — returns just that section"},
			"start_index": map[string]any{"type": "integer", "description": "character offset to continue reading from"},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		URL        string `json:"url"`
		Section    string `json:"section"`
		StartIndex int    `json:"start_index"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.URL == "" {
		return "", fmt.Errorf("url required")
	}
	if !strings.HasPrefix(a.URL, "http://") && !strings.HasPrefix(a.URL, "https://") {
		return "", fmt.Errorf("url must start with http:// or https://")
	}

	rawHTML, note, err := t.fetchHTML(ctx, a.URL)
	if err != nil {
		return "", err
	}

	// JS-required probe: if the first response is an empty SPA shell, render
	// once with the shipped headless Chromium and re-extract. Disclosed.
	if needsJS(rawHTML) {
		if rendered, rerr := renderWithChromium(ctx, a.URL); rerr == nil && len(rendered) > len(rawHTML) {
			rawHTML = rendered
			note += "[rendered with headless browser — the page required JavaScript]\n"
		}
	}

	md := extractMarkdown(rawHTML, a.URL)
	if strings.TrimSpace(md) == "" {
		return "", fmt.Errorf("the page produced no readable content")
	}

	return note + budget(md, a.Section, a.StartIndex), nil
}

// fetchHTML gets the page honestly and transcodes it to UTF-8. Failures come
// back as plain-language errors, never raw HTML or bare status codes.
func (t *WebFetch) fetchHTML(ctx context.Context, url string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", webFetchUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5")
	resp, err := t.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("could not reach %s: %w", hostOf(url), err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBody))
	switch {
	case resp.StatusCode == 404 || resp.StatusCode == 410:
		return "", "", fmt.Errorf("http 404: the page does not exist — check the URL")
	case resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode == 503:
		if isBotChallenge(string(body)) {
			return "", "", fmt.Errorf("http %d: %s blocks automated access (bot protection challenge). "+
				"This is the site's choice and Cerveau does not evade it — try the site's official API or ask the user to open it in a browser", resp.StatusCode, hostOf(url))
		}
		return "", "", fmt.Errorf("http %d: %s refused the request", resp.StatusCode, hostOf(url))
	case resp.StatusCode >= 400:
		return "", "", fmt.Errorf("http %d from %s", resp.StatusCode, hostOf(url))
	}

	// transcode whatever charset the server sent into UTF-8 for the parsers
	utf8Reader, err := charset.NewReader(strings.NewReader(string(body)), resp.Header.Get("Content-Type"))
	if err != nil {
		return string(body), "", nil // best effort: assume UTF-8
	}
	decoded, err := io.ReadAll(utf8Reader)
	if err != nil {
		return string(body), "", nil
	}
	return string(decoded), "", nil
}

// extractMarkdown isolates the main content (Readability) and converts it to
// markdown with code blocks and tables intact. If extraction comes back
// empty — some pages have no article structure — it falls back to converting
// the whole body rather than returning nothing.
// mdConverter carries the table plugin — the ConvertString convenience does
// not, and a coding agent that loses tables loses API references.
func mdConvert(html string) (string, error) {
	conv := converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(), commonmark.NewCommonmarkPlugin(), table.NewTablePlugin()))
	return conv.ConvertString(html)
}

func extractMarkdown(rawHTML, url string) string {
	pageURL, _ := nurl.Parse(url)
	content := rawHTML
	if article, err := readability.FromReader(strings.NewReader(rawHTML), pageURL); err == nil &&
		strings.TrimSpace(article.Content) != "" {
		content = article.Content
	}
	md, err := mdConvert(content)
	if err != nil || strings.TrimSpace(md) == "" {
		// last resort: whole page through the converter
		if md2, err2 := mdConvert(rawHTML); err2 == nil {
			return md2
		}
		return ""
	}
	// If readability gutted the page (kept <20% of a modest page), convert the
	// full body instead — better noisy than empty-handed.
	if len(md) < 200 && len(rawHTML) > 2000 {
		if md2, err2 := mdConvert(rawHTML); err2 == nil && len(md2) > len(md) {
			return md2
		}
	}
	return md
}

// budget shapes the markdown to the 32K-window consumer: small pages pass
// through; big pages become an outline; section/start_index drill in.
func budget(md, section string, startIndex int) string {
	if section != "" {
		if s, ok := extractSection(md, section); ok {
			return capWithContinuation(s, 0)
		}
		return fmt.Sprintf("no section titled %q — the outline is:\n%s", section, outline(md))
	}
	if startIndex > 0 {
		if startIndex >= len(md) {
			return fmt.Sprintf("[start_index %d is past the end — the document is %d chars]", startIndex, len(md))
		}
		return capWithContinuation(md[startIndex:], startIndex)
	}
	if len(md) <= webFetchCapChars {
		return md
	}
	return fmt.Sprintf("[%d chars total — too large for one call; outline below. "+
		"Call again with section=\"<heading>\" for one section, or start_index=<n> to read linearly]\n\n%s",
		len(md), outline(md))
}

var mdHeading = regexp.MustCompile(`(?m)^(#{1,3})\s+(.+)$`)

// outline lists the page's headings with the size of each section, so the
// model can decide where to spend its window.
func outline(md string) string {
	locs := mdHeading.FindAllStringSubmatchIndex(md, -1)
	if len(locs) == 0 {
		return fmt.Sprintf("(no headings — use start_index to read in %d-char chunks)", webFetchCapChars)
	}
	var b strings.Builder
	b.WriteString("outline:\n")
	for i, loc := range locs {
		level := loc[3] - loc[2]
		title := md[loc[4]:loc[5]]
		end := len(md)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		fmt.Fprintf(&b, "%s%s (%d chars)\n", strings.Repeat("  ", level-1), strings.TrimSpace(title), end-loc[0])
	}
	return strings.TrimRight(b.String(), "\n")
}

// extractSection returns the section whose heading matches (case-insensitive
// substring), from its heading to the next heading of the same or higher level.
func extractSection(md, name string) (string, bool) {
	locs := mdHeading.FindAllStringSubmatchIndex(md, -1)
	needle := strings.ToLower(strings.TrimSpace(name))
	for i, loc := range locs {
		title := strings.ToLower(strings.TrimSpace(md[loc[4]:loc[5]]))
		if !strings.Contains(title, needle) {
			continue
		}
		level := loc[3] - loc[2]
		end := len(md)
		for _, next := range locs[i+1:] {
			if next[3]-next[2] <= level {
				end = next[0]
				break
			}
		}
		return md[loc[0]:end], true
	}
	return "", false
}

func capWithContinuation(s string, base int) string {
	if len(s) <= webFetchCapChars {
		return s
	}
	return s[:webFetchCapChars] + fmt.Sprintf("\n[truncated — continue with start_index=%d]", base+webFetchCapChars)
}

// needsJS detects an SPA shell: almost no visible text and no server-rendered
// data blob means the content only exists after JavaScript runs.
func needsJS(rawHTML string) bool {
	for _, blob := range []string{"__NEXT_DATA__", "__NUXT__", "__INITIAL_STATE__", "application/ld+json"} {
		if strings.Contains(rawHTML, blob) {
			return false
		}
	}
	return len(visibleText(rawHTML)) < 500 && len(rawHTML) > 1000
}

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reTags        = regexp.MustCompile(`(?s)<[^>]+>`)
	reWs          = regexp.MustCompile(`\s+`)
)

func visibleText(s string) string {
	s = reScriptStyle.ReplaceAllString(s, " ")
	s = reTags.ReplaceAllString(s, " ")
	return strings.TrimSpace(reWs.ReplaceAllString(s, " "))
}

// renderWithChromium renders a JS-dependent page with the same headless
// binary check_page uses, and returns the post-render DOM.
func renderWithChromium(ctx context.Context, url string) (string, error) {
	chrome := findChrome()
	if chrome == "" {
		return "", fmt.Errorf("no headless browser available")
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, chrome,
		"--headless=new", "--no-sandbox",
		"--virtual-time-budget=6000", "--dump-dom", url)
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return "", err
	}
	return out.String(), nil
}

// isBotChallenge recognizes the common bot-protection interstitials so the
// error can say what actually happened instead of dumping challenge HTML.
func isBotChallenge(body string) bool {
	low := strings.ToLower(body)
	for _, marker := range []string{"just a moment", "cf-chl", "challenge-platform", "checking your browser", "turnstile", "attention required"} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

func hostOf(url string) string {
	if u, err := nurl.Parse(url); err == nil && u.Host != "" {
		return u.Host
	}
	return url
}
