package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CheckPage loads an HTML page in a headless browser and reports what actually
// happened: console errors, uncaught exceptions, and whether an expected
// element rendered. This is the feedback loop the model otherwise lacks — a
// broken page fails at RUNTIME in the browser, invisible to static reads.
// Debugging "the game doesn't render" without this means guessing.
//
// Implementation: headless chromium with --enable-logging=stderr (console
// messages and uncaught exceptions appear as CONSOLE lines) and --dump-dom
// (rendered DOM, for element checks). No node/playwright dependency.
type CheckPage struct {
	j jail
}

func NewCheckPage(workspaceRoot string) *CheckPage {
	return &CheckPage{j: newJail(workspaceRoot)}
}

func (t *CheckPage) Name() string { return "check_page" }

func (t *CheckPage) Description() string {
	return "Load an HTML page in a headless browser and report console errors, uncaught exceptions, " +
		"and whether an expected element rendered. USE THIS to verify web pages/apps actually work — " +
		"reading the source cannot reveal runtime errors. path: workspace-relative file, or url for a " +
		"served page. expect: optional element tag/id to confirm rendered (e.g. \"canvas\" or \"#board\")."
}

func (t *CheckPage) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "workspace-relative HTML file to load"},
			"url":    map[string]any{"type": "string", "description": "full URL to load instead of a file (e.g. a serve tool URL)"},
			"expect": map[string]any{"type": "string", "description": "element that must exist in the rendered DOM: a tag (canvas), #id, .class, or tag.class"},
		},
	}
}

// findChrome locates a usable headless chromium. Playwright's cache first
// (present on dev machines), then system binaries.
func findChrome() string {
	if env := os.Getenv("CRV_CHROME"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	if matches, _ := filepath.Glob(filepath.Join(home, ".cache/ms-playwright/chromium-*/chrome-linux*/chrome")); len(matches) > 0 {
		return matches[len(matches)-1] // highest version sorts last
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

var consoleLine = regexp.MustCompile(`INFO:CONSOLE[:(]\d+[)]?\]?\s*"(.*)", source: (\S+) \((\d+)\)`)

func (t *CheckPage) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path   string `json:"path"`
		URL    string `json:"url"`
		Expect string `json:"expect"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("bad args: %w", err)
	}
	if a.Path == "" && a.URL == "" {
		return "", fmt.Errorf("path or url required")
	}

	chrome := findChrome()
	if chrome == "" {
		return "", fmt.Errorf("no headless browser available on this machine")
	}

	target := a.URL
	if target == "" {
		full, err := t.j.resolve(a.Path)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(full); err != nil {
			return "", fmt.Errorf("%s does not exist", a.Path)
		}
		target = "file://" + full
	}

	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, chrome,
		"--headless=new", "--no-sandbox",
		// software WebGL: --disable-gpu would make every Three.js/canvas app
		// report "WebGL context could not be created" — a false failure.
		"--use-angle=swiftshader", "--enable-unsafe-swiftshader",
		"--enable-logging=stderr", "--v=0",
		"--virtual-time-budget=6000", // let scripts, timers and module loads run
		"--dump-dom", target,
	)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run() // chrome's exit code is unreliable; the output is the signal

	dom := out.String()
	var report strings.Builder

	// console errors + uncaught exceptions from the stderr log
	seen := map[string]bool{}
	errCount := 0
	for _, m := range consoleLine.FindAllStringSubmatch(errb.String(), -1) {
		msg, src, line := m[1], m[2], m[3]
		// trim the workspace prefix off file:// sources for readability
		src = strings.TrimPrefix(src, "file://"+t.j.root+"/")
		key := msg + src + line
		if seen[key] {
			continue
		}
		seen[key] = true
		errCount++
		if errCount <= 12 {
			fmt.Fprintf(&report, "  %s:%s  %s\n", src, line, msg)
		}
	}

	var final strings.Builder
	if errCount == 0 {
		final.WriteString("no console errors — the page loaded cleanly.\n")
	} else {
		fmt.Fprintf(&final, "%d console message(s)/error(s):\n", errCount)
		final.WriteString(report.String())
		if errCount > 12 {
			fmt.Fprintf(&final, "  ...and %d more\n", errCount-12)
		}
	}
	if dom == "" || !strings.Contains(dom, "<body") {
		final.WriteString("WARNING: the page produced no DOM — it may have failed before rendering.\n")
	}
	return checkExpect(final.String(), dom, a.Expect), nil
}

// checkExpect appends the expected-element verdict to the report.
func checkExpect(report, dom, expect string) string {
	if expect == "" {
		return strings.TrimRight(report, "\n")
	}
	// Accept the selector shapes models actually write: tag, #id, .class,
	// and tag.class — checked against the rendered DOM textually.
	var present bool
	sel := expect
	if i := strings.IndexByte(sel, '.'); i >= 0 && !strings.HasPrefix(sel, "#") {
		tag, class := sel[:i], sel[i+1:]
		present = classInDOM(dom, class) && (tag == "" || strings.Contains(dom, "<"+tag))
	} else if strings.HasPrefix(sel, "#") {
		present = strings.Contains(dom, `id="`+strings.TrimPrefix(sel, "#")+`"`)
	} else {
		present = strings.Contains(dom, "<"+sel)
	}
	if present {
		report += fmt.Sprintf("expected element %q: found in the rendered DOM.", expect)
	} else {
		report += fmt.Sprintf("expected element %q: NOT found — the page did not render it.", expect)
	}
	return report
}

// classInDOM reports whether any element carries the class (word match inside
// a class attribute, so "app" matches class="app shell").
func classInDOM(dom, class string) bool {
	for _, m := range reClassAttr.FindAllStringSubmatch(dom, -1) {
		for _, c := range strings.Fields(m[1]) {
			if c == class {
				return true
			}
		}
	}
	return false
}

var reClassAttr = regexp.MustCompile(`class="([^"]*)"`)
