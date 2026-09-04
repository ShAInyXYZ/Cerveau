package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	nurl "net/url"
	"os"
	"os/exec"
	"path"
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
		"served page. IF THE PAGE USES ES MODULES (<script type=\"module\">, import), path: WILL NOT WORK — " +
		"the browser blocks module loading over file://, so the page renders nothing. Start the serve tool " +
		"and pass its url instead; eval works there too. expect: optional element tag/id to confirm " +
		"rendered (e.g. \"canvas\" or \"#board\")."
}

func (t *CheckPage) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "workspace-relative HTML file to load"},
			"url":    map[string]any{"type": "string", "description": "full URL to load instead of a file (e.g. a serve tool URL)"},
			"expect": map[string]any{"type": "string", "description": "element that must exist in the rendered DOM: a tag (canvas), #id, .class, or tag.class"},
			"eval":   map[string]any{"type": "string", "description": "JS expression evaluated in the page after it loads (works with BOTH path: and url: — for an ES-module page use url:, since file:// cannot load modules); its value is returned to you. Use it to READ RUNTIME STATE, e.g. \"JSON.stringify({omega: window.__state.omega})\". Objects are JSON-stringified automatically. For multi-step tests: RETURN an array of results and push 'FAIL: <step> <state>' entries instead of throwing — one throw loses everything collected before it and tells you nothing. If you must throw, throw new Error('step X: from-to') so the report has a line number. EVERY CALL IS A FRESH PAGE LOAD: nothing set in one call exists in the next, so never schedule work and read it later. Put the whole sequence in ONE eval; if it needs frames or timers, use top-level await (allowed) and end with `return <results>`, or return a Promise — either is awaited up to 4 s. Variables declared inside a <script type=module> are NOT reachable from eval: read state through window.* / the DOM, or expose it (window.__state = …) in the page."},
		},
	}
}

// checkPageHostAllowed reports whether every resolved address of a URL's host
// is safe for check_page to load: loopback (the local serve tool) or a public
// address. LAN, link-local/metadata, CGNAT and private ranges are refused.
// Fails closed on parse or DNS error. Reuses publicIP from webfetch.go.
func checkPageHostAllowed(ctx context.Context, rawURL string) bool {
	u, err := nurl.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	allow := func(ip net.IP) bool { return ip.IsLoopback() || ipAllowed(ip) }
	if ip := net.ParseIP(host); ip != nil {
		return allow(ip)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !allow(ip.IP) {
			return false
		}
	}
	return true
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
		Eval   string `json:"eval"`
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
	} else {
		// SSRF guard: a model-supplied url is loaded in a real browser (which
		// can fetch subresources) AND may carry an eval that reads the page
		// back. The intended use is checking the local `serve` tool, which
		// binds 127.0.0.1 — so LOOPBACK is allowed (it is the agent's own
		// server, same trust as the workspace). Everything else non-public
		// (LAN, link-local/metadata, private ranges) is blocked: those are
		// OTHER hosts the model must not be able to reach and read back.
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			return "", fmt.Errorf("url must start with http:// or https:// (use path for a workspace file)")
		}
		if !checkPageHostAllowed(ctx, target) {
			return "", fmt.Errorf("refusing to load a non-public, non-loopback address (LAN/link-local/metadata blocked)")
		}
	}

	// eval: wrap the page so the expression runs after load and its value is
	// printed to the console, which we already capture. Chromium headless has
	// no --evaluate flag, and a wrapper is why this needs no browser driver:
	// the model asked for playwright 26 times in one run because it could not
	// read runtime state any other way.
	// The eval harness runs a COPY of the page (.crv-eval-*.html, deleted on
	// return). Every path in the report must name the real file: a model that
	// is told "index.html:901" fixes index.html; one told ".crv-eval-331.html:901"
	// spends the next ten iterations editing a file that no longer exists
	// (2026-09-04, four identical turns of "temp file gone").
	unleak := func(s string) string { return s }
	if a.Eval != "" {
		wrapped, cleanup, werr := t.writeEvalHarness(target, a.Eval)
		if werr != nil {
			return "", werr
		}
		defer cleanup()
		// Both forms end in a file name; take it off the URL path or the
		// file path so the report names the page the model can actually edit.
		tmpBase := pageBase(wrapped)
		origBase := pageBase(target)
		unleak = func(s string) string { return strings.ReplaceAll(s, tmpBase, origBase) }
		target = wrapped
	}

	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, chrome,
		"--headless=new", "--no-sandbox",
		// software WebGL: --disable-gpu would make every Three.js/canvas app
		// report "WebGL context could not be created" — a false failure.
		"--use-angle=swiftshader", "--enable-unsafe-swiftshader",
		"--enable-logging=stderr", "--v=0",
		"--virtual-time-budget=12000", // load + the 2.5 s settle + up to 4 s of awaited eval
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
		msg, src, line := unleak(m[1]), unleak(m[2]), m[3]
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

	// pull the eval result out of the console stream
	evalResult := ""
	for _, ln := range strings.Split(errb.String(), "\n") {
		if i := strings.Index(ln, evalMarker); i >= 0 {
			evalResult = unleak(strings.TrimSpace(ln[i+len(evalMarker):]))
		}
	}

	var final strings.Builder
	if a.Eval != "" {
		if evalResult != "" {
			fmt.Fprintf(&final, "eval result: %s\n", evalResult)
		} else {
			final.WriteString("eval produced no result — the expression may have thrown before the page settled.\n")
		}
	}
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

// evalMarker tags the eval result so it can be pulled out of ordinary console
// noise. A page that logs a lot would otherwise bury the answer.
const evalMarker = "__CRV_EVAL__"

// writeEvalHarness copies the page and appends a script that evaluates the
// expression in ITS OWN context, then logs the result.
//
// An iframe cannot work here: file:// documents are cross-origin with each
// other (origin "null"), so contentWindow access is blocked. Appending to a
// copy keeps everything same-document, and the copy sits in the workspace so
// relative paths (modules, textures, importmaps) still resolve.
func (t *CheckPage) writeEvalHarness(target, expr string) (string, func(), error) {
	// A served URL is the ONLY way to check a page that uses ES modules:
	// over file:// the browser treats every module as cross-origin and
	// refuses to load it, so the page renders nothing and every probe
	// reports "no canvas". Refusing eval here used to leave the model with
	// two half-tools — url: could see the page but not probe it, path: could
	// probe but never loaded the modules — and it burned eight iterations
	// discovering that before the guard killed the turn (2026-09-04, fan).
	//
	// The copy therefore goes next to the real file and is requested through
	// the SAME server, so it shares the page's origin and its relative
	// imports resolve exactly as they do for the real page.
	if !strings.HasPrefix(target, "file://") {
		orig, rewrite, err := t.servedFile(target)
		if err != nil {
			return "", func() {}, err
		}
		tmp, cleanup, err := t.harnessBeside(orig, expr)
		if err != nil {
			return "", cleanup, err
		}
		return rewrite(filepath.Base(tmp)), cleanup, nil
	}
	name, cleanup, err := t.harnessBeside(strings.TrimPrefix(target, "file://"), expr)
	if err != nil {
		return "", cleanup, err
	}
	return "file://" + name, cleanup, nil
}

// servedFile maps a loopback URL from the `serve` tool back to the file it
// serves, and returns a rewrite that turns a sibling file name into the URL
// that reaches it. The harness copy must be fetched over HTTP, not read off
// disk: same origin is the whole point.
func (t *CheckPage) servedFile(rawURL string) (string, func(string) string, error) {
	u, err := nurl.Parse(rawURL)
	if err != nil {
		return "", nil, fmt.Errorf("eval harness: %w", err)
	}
	// "/" and "/sub/" serve an index; name it so the copy lands beside it.
	upath := u.Path
	if upath == "" || strings.HasSuffix(upath, "/") {
		upath += "index.html"
	}
	full, err := t.j.resolve(strings.TrimPrefix(upath, "/"))
	if err != nil {
		return "", nil, fmt.Errorf("eval over url: %s is not inside the workspace, so there is nothing to instrument — serve the workspace and pass that URL", upath)
	}
	if _, err := os.Stat(full); err != nil {
		// The server may be rooted at a subdirectory (serve dir=...), so the
		// URL path alone does not locate the file. Say so plainly instead of
		// failing with a bare stat error.
		return "", nil, fmt.Errorf("eval over url: cannot find the file behind %s in the workspace (if the server was started with dir=, pass a url whose path is workspace-relative, or use path: for a page without ES modules)", rawURL)
	}
	rewrite := func(base string) string {
		c := *u
		c.Path = path.Join(path.Dir(upath), base)
		return c.String()
	}
	return full, rewrite, nil
}

// harnessBeside writes the instrumented copy in the page's own directory, so
// relative imports, import maps and assets resolve the way they do for the
// real page.
func (t *CheckPage) harnessBeside(orig, expr string) (string, func(), error) {
	body, err := os.ReadFile(orig)
	if err != nil {
		return "", func() {}, fmt.Errorf("eval harness: %w", err)
	}

	f, err := os.CreateTemp(filepath.Dir(orig), ".crv-eval-*.html")
	if err != nil {
		return "", func() {}, fmt.Errorf("eval harness: %w", err)
	}
	name := f.Name()
	cleanup := func() { _ = os.Remove(name) }

	probe := `
<script>
setTimeout(function () {
  // The expression may return a Promise (a test that dispatches events and
  // waits for frames). It is awaited, up to 4 s, so an async test reports
  // its real outcome instead of "scheduled" — a crane build spent eight
  // calls reading results that a fresh page load had never produced.
  function report(v) {
    if (v && typeof v === 'object') { try { v = JSON.stringify(v); } catch (e) { v = String(v); } }
    console.log(` + jsString(evalMarker) + ` + ' ' + v);
  }
  var v;
  try {
    try {
      v = eval(` + jsString(expr) + `);
    } catch (se) {
      // Top-level await: plain eval rejects it, and the model reaches for it
      // as soon as it tests anything that takes frames. Re-run the same text
      // as the body of an async function; its return value is awaited below.
      if (se instanceof SyntaxError && /await|return/i.test(String(se.message))) {
        v = eval('(async function () {' + ` + jsString(expr) + ` + '\n})()');
      } else { throw se; }
    }
    if (v && typeof v.then === 'function') {
      var timer = new Promise(function (res) { setTimeout(function () { res('EVAL ERROR: promise did not settle within 4 s'); }, 4000); });
      Promise.race([v, timer]).then(report, function (e) { report('EVAL ERROR: ' + (e && e.message ? e.message : String(e))); });
      return;
    }
  } catch (e) {
    // Say WHERE it threw, not just what: a thrown Error carries a stack with
    // the line inside the eval; a thrown string carries nothing, so say so.
    var where = '';
    if (e && e.stack) { var m = String(e.stack).match(/<anonymous>:(\d+):(\d+)/); if (m) where = ' (eval line ' + m[1] + ':' + m[2] + ')'; }
    var msg = e && e.message ? e.message : String(e);
    if (!(e instanceof Error)) msg += ' [a bare value was thrown — throw new Error(\'what failed and where\') to get a line number, or return partial results instead of throwing]';
    v = 'EVAL ERROR: ' + msg + where;
  }
  report(v);
}, 2500);
</script>`
	if _, err := f.Write(append(body, []byte(probe)...)); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("eval harness: %w", err)
	}
	f.Close()
	return name, cleanup, nil
}

// pageBase is the file name a target ends in, for either an http(s) URL or a
// file path. Used only to rewrite the temp harness name back to the real page
// in the report, so a query string or fragment must not become part of it.
func pageBase(target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		if u, err := nurl.Parse(target); err == nil {
			return path.Base(u.Path)
		}
	}
	return filepath.Base(strings.TrimPrefix(target, "file://"))
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func htmlAttr(s string) string {
	return strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", `<`, "&lt;", `>`, "&gt;").Replace(s)
}
