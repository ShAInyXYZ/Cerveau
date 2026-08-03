# web_fetch — clean web content for a 32K-context model (v0.3)

`web_fetch` turns a URL into clean markdown sized for Cerveau's small local
model. It replaces the naive strip-all-tags GET from v0.2. The design follows
the research memo in `webresearch_research.md`; this document records what was
actually built and why.

## The pipeline

```
honest HTTP GET ── charset transcode ── needs-JS probe ──┐
                                                         ├─ Readability ─ html-to-markdown ─ budgeter
headless Chromium render (only if the probe says so) ────┘   (main content)  (code+tables kept)  (outline/section/offset)
```

This is the same single-page core Jina Reader and Firecrawl run behind their
APIs (Readability → markdown converter), in-process with zero services.

## Elements added

| Element | Implementation | Why |
|---|---|---|
| Honest User-Agent | `Cerveau/0.3 (local coding agent; +https://cerveau.sh)` | Legitimacy by identification, never impersonation. Spoofed browser strings are what bot detection punishes hardest. |
| Charset transcoding | `golang.org/x/net/html/charset.NewReader` | Non-UTF-8 pages (Latin-1, Shift-JIS) crash or mojibake `x/net/html`-based parsers; sniffs meta tags + BOM. |
| Main-content extraction | `go-shiori/go-readability` (Readability.js port) | Strips nav/footer/cookie-banner boilerplate — the noise that poisons a small model's window. Chosen over the readeck fork for the stable module path; the algorithm itself is frozen. Swap if the golden set ever regresses. |
| Markdown conversion | `JohannesKaufmann/html-to-markdown/v2` with **base + commonmark + table plugins** | Code blocks stay fenced, tables stay tables. The `ConvertString` convenience omits the table plugin — the explicit converter is deliberate. |
| Empty-extraction fallback | Readability yields nothing (or gutted <200 chars of a real page) → convert the full body | Firecrawl's trick: never return empty-handed when a mechanical fallback can return something. |
| Token budgeting | ≤8,000 chars pass through; larger pages return an **outline** (headings + per-section char cost); `section="<heading>"` drills in; `start_index=<n>` reads linearly | The binding constraint for a 32K/3B consumer is window budget, not extraction quality. Same pattern as the `read` tool's capped output + continuation. |
| Needs-JS probe | <500B visible text AND no hydration blob (`__NEXT_DATA__`, `__NUXT__`, `__INITIAL_STATE__`, ld+json) | Doc sites ship content in the first response; only true SPA shells pay the browser cost. |
| Chromium fallback | The shipped headless binary (same one as `check_page`), `--dump-dom --virtual-time-budget=6000` | Zero new dependencies; disclosed in the output as `[rendered with headless browser]`. |
| Friendly failure taxonomy | 404 → "the page does not exist"; 403/429/503 + challenge markers (`Just a moment`, `cf-chl`, `turnstile`…) → "site blocks automated access" | The model must never see challenge HTML or bare status codes — those trigger useless retries. A blocked site is stated as blocked, with the advice to use the site's official API. |

## The legitimacy stance

- **Identify honestly.** Descriptive UA with a contact URL, standard Accept
  header. No browser impersonation, no TLS/JA3 fingerprint spoofing, ever.
- **Do not fight bot protection.** Cloudflare-class blocking keys on TLS
  fingerprint and IP reputation before the request reaches the page. The
  sanctioned paths (Verified Bots, Web Bot Auth) target crawler operators, not
  local agents. When a site says no, `web_fetch` says the site said no.
- **The real corpus doesn't need evasion.** GitHub, MDN, docs.python.org,
  pkg.go.dev, PyPI, Read-the-Docs all serve honest plain clients (verified in
  the research memo and by the golden-set run below).
- Watch **Web Bot Auth** (IETF HTTP Message Signatures for bots): if it
  matures for individual agents, request-signing is the principled upgrade.

## Verified (golden set, live run at build time)

| Page | Result |
|---|---|
| MDN `404` reference | 2,959 chars clean markdown, 6 fenced code blocks |
| GitHub README (html-to-markdown) | 17,949 chars → outline with 8 sections + per-section sizes; `section="Usage"` returns just that section |
| pkg.go.dev `net/http` | 112,267 chars → outline (the RFC-class page that would otherwise blow the whole window) |

Unit tests: extraction fidelity (fenced code, tables, boilerplate removal),
honest-UA enforcement (fails if the UA ever contains `mozilla`/`chrome`),
outline + section drill-down + `start_index`, friendly 404/challenge errors,
empty-extraction fallback. See `internal/tools/webfetch_test.go`.

## Dependencies added (all lean, no services)

- `github.com/go-shiori/go-readability`
- `github.com/JohannesKaufmann/html-to-markdown/v2`
- `golang.org/x/net` (charset; transitively shared by both)

## Not built, deliberately

- TLS fingerprint impersonation / proxy rotation — evasion, off the table.
- robots.txt enforcement — this is a user-directed single-page fetch, not a
  crawler; rates are single-shot. Revisit if bulk crawling is ever added.
- Neural extraction (ReaderLM-class) — worse on docs (0.776 vs 0.93 F1) and
  ~100× slower; wrong for local CPU-adjacent hardware.
- A crawling framework — single-page on-demand is the use case; colly and
  Firecrawl-as-a-service solve a different problem.
