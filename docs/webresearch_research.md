# Recommendation Memo: Web-Data-Fetching for Cerveau

## TL;DR
- **Build, don't adopt.** A ~250–400 line in-process Go tool wins: HTTP-first fetch → `go-shiori/go-readability` (or its maintained `readeck/go-readability` fork) for main-content isolation → `JohannesKaufmann/html-to-markdown/v2` with the table plugin, plus a lazy headless-Chromium fallback (reuse the shipped binary via `--headless=new --dump-dom`) gated by a cheap "does this need a browser?" probe. This is literally "the 20% of Firecrawl/Jina that matters, in-process" — because Jina Reader and Firecrawl's single-page cores are exactly *Readability + a markdown converter*.
- **Extraction quality is largely solved for your corpus; token budgeting is the binding constraint.** On the WCXB benchmark, documentation pages score F1 0.88–0.93 for the top rule-based extractors — nearly as high as articles — so a marginally better extractor buys little. But even after clean extraction, a typical MDN reference page is ~4,600 tokens and a long GitHub README or RFC can be 10K–100K+ tokens. For a 32K-context / 3B model, sectioning + outline-first + `start_index`-style pagination is mandatory, not optional.
- **Anti-bot barely bites your real targets.** MDN, docs.python.org, pkg.go.dev, PyPI, Read-the-Docs, and GitHub raw/API all serve a plain Go HTTP client with an honest User-Agent. The exceptions are Cloudflare-fronted blog/Q&A content (Stack Overflow, Substack, Medium's JS paywall) — and headless Chromium does **not** reliably fix those (TLS/JA3 fingerprinting blocks all raw clients, compounded by datacenter-IP reputation). Accept partial coverage rather than adding utls/curl-impersonate complexity.

## Key Findings

1. **Best-in-class extractor for code/docs pages: the trafilatura family or Readability, and the gap between them is small on documentation.** The WCXB multi-type benchmark (2,008 pages, 7 page types, released 2026) is the first to score documentation pages separately. On its documentation subset (91 pages): rs-trafilatura 0.931, Trafilatura 0.888, Resiliparse 0.883, MinerU-HTML 0.838, ReaderLM-v2 0.776. Mozilla Readability scores lower overall (0.674 dev F1) but is the proven, battle-tested engine that Jina and Firecrawl actually ship.
2. **Go-native reality:** `go-shiori/go-readability` was **archived Dec 30, 2025** (read-only; 941 stars, 99 forks) — active development moved to the `readeck/go-readability` fork (Codeberg), which states development "continues in the v2 branch, which you should choose for best speed and memory efficiency" and remains "compatible with Readability.js v0.6.0." `markusmobius/go-trafilatura` is a full Go port of trafilatura (uses go-readability + go-domdistiller as fallbacks). `JohannesKaufmann/html-to-markdown/v2` handles code blocks and tables (v2 table plugin added 2024). These three are the core building blocks.
3. **JS-fallback cascade is cheap to build and mostly unnecessary.** A stdlib-style probe reading three signals from the raw HTTP response — visible-text size, presence of a hydration blob (`__NEXT_DATA__`, `__NUXT__`, `application/ld+json`), and (if known) a needle string — correctly routed 6/10 real pages to "no browser." Doc sites (Wikipedia, python.org, GitHub) ship their data in the first response.
4. **Firecrawl and Jina Reader both use Readability + a markdown converter — reproducible locally.** Jina Reader (`jina-ai/reader`, Apache-2.0, TypeScript): headless Chrome → Mozilla Readability.js → Turndown + regex. Firecrawl (`firecrawl/firecrawl`, AGPL): `onlyMainContent` uses the Mozilla Readability algorithm, then a markdown converter, with cheerio for metadata/links.
5. **Token budgeting dominates.** Claude Code's WebFetch converts HTML→markdown via Turndown, truncates to 100KB (~15–20K words), then runs a small fast model with the user's prompt to extract only relevant parts. The MCP fetch server truncates to 5,000 chars by default and offers `start_index` continuation. These are the patterns to copy.

## Details

### 1. Content extraction — what actually wins, and does the benchmark transfer? (Stress-test A)

The classic extraction benchmarks (ScrapingHub/Zyte, trafilatura's own eval, dragnet's) are scored predominantly on news/blog articles. The strongest counter-evidence is the **Web Content Extraction Benchmark (WCXB)**, a 2026 benchmark of 2,008 annotated pages across 7 page types including a dedicated **documentation** category (91 pages). Its central finding: *on articles every top system converges (F1 0.88–0.93); on other page types they diverge by 20–30 points.*

Crucially for Cerveau, **documentation is one of the page types where extraction still works well.** WCXB documentation-subset F1:

| System | Article F1 | Documentation F1 |
|---|---|---|
| rs-trafilatura | 0.932 | 0.931 |
| Trafilatura | 0.924 | 0.888 |
| Resiliparse | 0.871 | 0.883 |
| MinerU-HTML (neural 0.6B) | 0.928 | 0.838 |
| ReaderLM-v2 (neural 1.5B) | 0.880 | 0.776 |

So the benchmark ranking *does* substantially transfer to docs. The page types where article-tuned extractors fall apart are **forums** (Trafilatura 0.575) and **collections/products** — and Stack Overflow is structurally a forum/multi-answer page, so extraction quality there is the genuine weak spot, not docs. Neural HTML→markdown models (ReaderLM-v2) are both slower (~10,410 ms/page on an A100 vs 28–97 ms for rule-based) and *worse* on docs — decisively wrong for a local 3B-scale tool.

**Code blocks and tables** are a markdown-converter concern more than an extraction concern. `JohannesKaufmann/html-to-markdown/v2` explicitly "correctly handles backticks and multi-line code blocks, preserving code structure," and its v2 table plugin "converts tables with support for alignment, rowspan and colspan." This is the single most important library choice for a coding agent: Readability/trafilatura preserve the `<pre>/<code>/<table>` structure, but the converter decides whether it survives to markdown.

**Verdict:** Use a Readability/trafilatura-family extractor to isolate main content, then html-to-markdown/v2 with the table + commonmark plugins. Do not chase a marginally higher extraction F1; on your corpus (docs) the top systems are within a few points.

### 2. Go-native candidate survey (maintenance, deps, encoding)

- **`go-shiori/go-readability`** — line-by-line port of Readability.js, compatible with Readability.js v0.6.0. **Archived Dec 30, 2025**, read-only (941 stars, 99 forks), ~18 imports. Still usable (Readability's algorithm is stable), but for a living dependency prefer **`readeck/go-readability`** (Codeberg), the maintained fork whose development "continues in the v2 branch... for best speed and memory efficiency." Encoding: go-readability leans on `golang.org/x/net/html`, which requires UTF-8; you must sniff/convert charset yourself (`golang.org/x/net/html/charset`).
- **`markusmobius/go-trafilatura`** — full trafilatura port (~4,000-LOC lineage), uses go-readability + go-domdistiller as fallbacks, compiles hot regexes via re2go for speed. Heavier dependency graph (pulls goquery, lru, etc.). Handles tables/links/images as options (`--no-tables`, `--links`, `--images`). Higher extraction ceiling than bare Readability but more transitive weight.
- **`JohannesKaufmann/html-to-markdown/v2`** — the markdown converter. Built on `golang.org/x/net/html`. Actively maintained (v2.2.x, dependabot-tracked). Plugins for commonmark, tables, strikethrough. This is the keeper.
- **`PuerkitoBio/goquery`** — jQuery-like DOM traversal; requires Go 1.25+ as of v1.12.0 (2026-03). Useful for hand-rolled extraction or pre-cleaning, but requires UTF-8 input (caller's responsibility).
- **`colly`** — a crawling framework; overkill for single-page on-demand fetches. Reject.
- **`chromedp` / `go-rod`** — CDP client libraries. Both are heavy dependencies. Rod ships a pinned Chromium and is decode-on-demand (faster on heavy network events); chromedp is pure-Go with no third-party deps but uses the system browser (and can break if the browser auto-updates). **For Cerveau, neither is needed:** the system already ships a headless-Chromium binary used with `--headless=new --dump-dom`. Shelling out to that binary for the rare JS-render case costs zero new dependency. A CDP library only earns its weight if you need fine-grained DOM-ready waiting; for "render and dump DOM," `--dump-dom` with a `--virtual-time-budget` flag suffices.

**Encoding/charset/compression:** Go's `net/http` transport transparently handles gzip when you don't set `Accept-Encoding` yourself; brotli requires `andybalholm/brotli` or manual setup. Non-UTF-8 pages (meta charset, Latin-1, Shift-JIS) must be detected and transcoded before handing HTML to any `x/net/html`-based parser — `golang.org/x/net/html/charset.NewReader` does meta-tag sniffing and BOM detection. This is a real edge case a naive GET misses; it must live in the ~30 lines of fetch code.

### 3. JS-rendering decision rule and latency (Question 2)

The cleanest cascade is **HTTP-first, browser-on-fallback**, gated by a cheap probe on the raw response. The three signals that work:

1. **Visible-text size** after stripping `<script>/<style>` and tags. An empty SPA shell leaves <500 bytes (e.g., `www.reddit.com`'s new frontend returned **37 bytes** of visible text); a real doc page leaves tens of KB (Wikipedia 30,581 bytes).
2. **Hydration blob presence:** `__NEXT_DATA__`, `__NUXT__`, `window.__INITIAL_STATE__`, `__APOLLO_STATE__`, `<script type="application/ld+json">`. If present, the data is already in the first response — no browser needed (Wikipedia, python.org, and the Scrapy GitHub page all showed `blob=True`).
3. **Root-div-only heuristic:** a known SPA mount (`#root`, `#__next`, `#app`) with near-empty text is a strong JS-required signal.

A blunt rule works: `text < 500B and no blob → JS_REQUIRED; text ≥ 2000B or blob → NO_BROWSER; else MAYBE`. In a real 10-URL run this routed 6/10 correctly to no-browser, 2 to JS-required (both new-Reddit SPA), 2 to MAYBE. For a docs-heavy corpus the no-browser fraction will be far higher.

**Latency per path:**
- Plain HTTP GET + extract + convert: well under the ~3s target — rule-based extractors run ~28–97 ms/page (per WCXB timings); network fetch dominates, typically a few hundred ms. Total commonly <1s.
- Headless Chromium `--dump-dom`: cold Chromium process start dominates (~0.3–1s) plus render/network. A one-shot `--headless=new --dump-dom` per call is simplest but adds ~1–2s; a warm/persistent instance amortizes it. Firecrawl reports a **P95 latency of 3.4s** across its (browser-heavy) pipeline, which sets a realistic ceiling for the rendered path.

Route `MAYBE` to a single browser sample, not a blanket render. The probe's known failure modes: scroll/lazy-loaded content (a fat HTML head can still hide the rows you want) and anti-bot walls (a TLS handshake timeout tells you a bare client won't work at all).

### 4. What Firecrawl / Jina Reader actually do (Question 4)

- **Jina Reader (`jina-ai/reader`, Apache-2.0, TypeScript):** verbatim from Jina's own Reader-LM announcement — "First, we use a headless Chrome browser to fetch the source of the webpage. Then, we leverage Mozilla's Readability package to extract the main content... Finally, we convert the cleaned-up HTML into markdown using regex and the Turndown library." That is the entire core pipeline — reproducible in Go with go-readability + html-to-markdown in well under 500 lines. (Jina's later ReaderLM/ReaderLM-v2 replaces this with a small LM, but that's the slow/GPU path and scores *worse* on docs — skip it. Note Elastic completed its acquisition of Jina AI in October 2025, and keyless r.jina.ai access is capped at 20 requests/minute.)
- **Firecrawl (`firecrawl/firecrawl`, AGPL, TypeScript):** its `deriveHTMLFromRawHTML` transformer's `onlyMainContent` option "extracts main content using **Mozilla Readability algorithm**"; `deriveMarkdownFromHTML` then converts via `parseMarkdown`; cheerio handles metadata/links/images. A reproducible-locally trick worth copying: **if `onlyMainContent` yields empty markdown, retry with `onlyMainContent: false`** (fall back to full-page conversion rather than returning nothing). Everything else Firecrawl does (proxy rotation, server-side JS rendering, LLM JSON extraction, crawl orchestration, indexing) is out of scope for single-page on-demand fetch.
- **Others:** **Defuddle** (`kepano/defuddle`, from the Obsidian Web Clipper team) is a Readability alternative explicitly designed for markdown, standardizing code blocks (strips line numbers, keeps language tags), math, and footnotes, with site-specific extractors (GitHub, Reddit, ChatGPT) — the most "code-block-aware" extractor, but TypeScript/JSDOM, so not in-process for Go. **MarkItDown/Docling/Crawl4AI** are Python and heavier. **Postlight/Mercury parser** is effectively deprecated.

The takeaway: the industry-standard single-page pipeline is *Readability → markdown converter*, exactly what a Go port can do in-process with zero services.

### 5. Anti-bot reality check (Question 5)

Per-site verdicts for a plain Go HTTP client with an honest (non-`python-requests`, non-empty) User-Agent:

| Site | Verdict | Mechanism |
|---|---|---|
| github.com / api / raw.githubusercontent.com | **WORKS** | Fastly, plain 200 (docs.github.com is the exception — a UA regex returns 403 to bot-like UAs; per GitHub Docs issue #17042, `curl --head` → HTTP/2 403 while a full browser-UA → 200) |
| developer.mozilla.org (MDN) | **WORKS** | server-rendered HTML, no wall |
| docs.python.org | **WORKS** | static Sphinx, no wall |
| pkg.go.dev | **WORKS** | plain 200; as of June 2026 also has an official HTTP/MCP API for agents |
| readthedocs.io | **WORKS** | static docs |
| pypi.org | **WORKS** | official JSON/Index APIs, plain 200 |
| npmjs.com | **WORKS** | registry.npmjs.org is a public JSON API; website is JS-heavier |
| stackoverflow.com | **SOMETIMES** | Cloudflare Bot Management + Turnstile; IP-reputation dependent |
| medium.com | **SOMETIMES/BLOCKED** | JS paywall; member-locked content not served to plain clients (agents route via Freedium mirror) |
| substack.com | **SOMETIMES/BLOCKED** | Cloudflare; documented 403s even with browser UA + cookies (yt-dlp issue #15213) |

The critical design insight: **headless Chromium does not solve the hard cases.** Cloudflare Bot Management keys on TLS/JA3 fingerprint and IP reputation *before* your request reaches the page; a raw Go client fails the handshake test regardless of User-Agent, and headless-Chromium-from-a-datacenter-IP still presents a low-trust IP. The only reliable fixes (utls/JA3 impersonation, residential proxies, curl-impersonate) violate Cerveau's zero-services/minimal-deps philosophy. Since the *actual* target corpus (docs, MDN, GitHub, PyPI, pkg.go.dev) serves plain clients fine, the right call is: **honest User-Agent, HTTP-first, headless fallback for legitimately JS-rendered public pages, and graceful failure with a clear "blocked by bot protection" message for the Cloudflare-walled minority.** Do not build fingerprint evasion.

Broader context: Cloudflare "currently manages traffic for about 20% of the World Wide Web," and "on July 1, 2025, Cloudflare began blocking crawlers by default" (per Fortune's Change the World 2025 profile), with over 1M customers opting to block AI crawlers between Sept 2024 and July 2025. Cloudflare's "Verified bots" program is the sanctioned path for a well-behaved agent (honest self-identification, robots.txt compliance, reasonable rates) — worth honoring in the User-Agent string and request behavior even though verification isn't required for a local tool.

### 6. Token budgeting — the real binding constraint (Stress-test B, Question 6)

Even with perfect extraction, target pages routinely exceed a comfortable fraction of a 32K window. Measured token counts (HTML→text; a good extractor gets you close to the "text" column):

- **MDN HTTP status reference:** ~62,411 tokens raw HTML → ~4,649 tokens naive text → ~2,703 tokens after boilerplate stripping (a 42% reduction *after* tags are gone — naive extraction still leaves lots of nav/boilerplate).
- **docs.python.org json library page:** ~32,245 HTML → ~6,271 text tokens.
- **Wikipedia article:** ~48,975 HTML → ~6,658 text → ~6,234 stripped.
- **RFC 9110 (a long single-page spec):** ~378,840 HTML → ~110,723 text tokens — massively over any 32K budget.
- Median HTML→text multiplier across 10 real pages was **7.4×**, ranging 1.1× to 47.8×.

So a typical MDN/Python reference page lands at **~3K–6K clean tokens** — fine on its own, but you'll often want 2–4 of them, and a long README, RFC, or single-page API reference blows the 32K window by itself. For a 3B model with 32K context, **this is the binding constraint, not extraction quality.** After best-in-class extraction, pages are routinely >5K and not-rarely >10K tokens.

**Therefore build sectioning/pagination, not a better extractor.** Prior art to copy:
- **MCP fetch server** (`modelcontextprotocol/servers`): "max_length (integer, optional): Maximum number of characters to return (default: 5000)" and a `start_index` so the model can "read a webpage in chunks, until they find the information they need." It uses `readabilipy` + `markdownify` under the hood.
- **Claude Code WebFetch:** Turndown HTML→markdown, truncate to 100K chars, then a small fast model extracts only the query-relevant parts (prompt-driven). It *skips* the summarizer if content is already markdown and <100K chars.

For Cerveau's 3B/32K model, the recommended output is a three-tier design analogous to a file-read tool:
1. **Outline first:** return the heading tree (H1–H3) + token cost per section (~200 tokens), letting the model decide where to drill.
2. **Section drill-down:** fetch a named section (or `start_index`/offset) on request, capped (e.g., 4–6K tokens), **keeping code blocks verbatim** and, if over budget, dropping prose paragraphs *before* dropping code/tables (code is why a coding agent fetched the page).
3. **Continuation:** `start_index`-style offset for the rare page that needs linear reading.

This is more valuable to build than any extractor upgrade.

## Recommendations

**Stage 1 — Ship the core (~1–2 days, highest leverage):**
1. Fetch: `net/http` GET with an honest, descriptive User-Agent (e.g., `Cerveau/1.0 (+local coding agent)`; avoid the `Go-http-client` default and never send `python-requests`-style strings). Handle gzip (automatic) and charset transcoding via `golang.org/x/net/html/charset.NewReader`.
2. Extract: `readeck/go-readability` (maintained fork) or `go-shiori/go-readability` (archived-but-stable) to isolate main content.
3. Convert: `JohannesKaufmann/html-to-markdown/v2` with commonmark + table plugins. Verify code-block and table fidelity against a golden set of MDN, a GitHub README, a pkg.go.dev page, and a Read-the-Docs API page.
4. Adopt Firecrawl's empty-result fallback: if main-content extraction yields near-empty markdown, re-run the converter on the full cleaned body.

**Stage 2 — Token budgeting (build before optimizing extraction):**
5. Add heading-based sectioning + outline-first output + `start_index`/section-offset continuation, capped at ~4–6K tokens per call, code/tables preserved preferentially over prose. Mandatory for a 32K/3B consumer.

**Stage 3 — JS fallback (only after measuring you need it):**
6. Add the 3-signal probe (text size, hydration blob, SPA root div). Route only `JS_REQUIRED`/`MAYBE` to the shipped headless-Chromium via `--headless=new --dump-dom --virtual-time-budget=…`, then feed the dumped DOM into the same extract→convert path. Do not add chromedp/rod unless you later need DOM-ready waiting.

**Stage 4 — Graceful degradation:**
7. Detect Cloudflare/Turnstile/403 challenge pages and return a clear "site blocked automated access" result rather than dumping challenge HTML into the model's context.

**Component sketch (deliverable d):**
- **Inputs:** `url string`, optional `section string` / `start_index int`, optional `max_tokens int`.
- **Outputs:** clean markdown (or a heading outline with per-section token costs on the first call); metadata (title, canonical URL, status); a `truncated`/`next_index` signal.
- **Size:** ~250–400 lines of Go — ~30 for fetch/charset/gzip, ~40 for the JS probe, ~30 for shelling to Chromium, ~60 for the extract→convert glue + empty-result fallback, ~120–180 for sectioning/outline/pagination.
- **Dependencies:** `readeck/go-readability` (or `go-shiori/go-readability`), `JohannesKaufmann/html-to-markdown/v2`, and `golang.org/x/net/html` + `.../charset` (transitively pulled by both). All lean; both extractor and converter are built on `x/net/html`, so the shared transitive surface is small. No Redis, queues, Docker, or SaaS. Chromium is the already-shipped binary, invoked via `os/exec`.

**Benchmarks/thresholds that would change the plan:**
- If golden-set testing shows go-readability mangles code-bearing pages (dropping `<pre>` blocks), switch to `markusmobius/go-trafilatura` (higher ceiling, heavier deps) or pre-clean with goquery before conversion.
- If usage shifts toward Stack Overflow / forum pages, revisit: extractors score ~0.58–0.79 there vs 0.88+ on docs, so a forum-specific path (or SO's API) becomes worthwhile.
- If measured pages routinely exceed 10K tokens (RFCs, giant READMEs, single-page API refs), prioritize sectioning depth over everything else.
- If you're frequently blocked on Cloudflare content users actually need, that's the signal to consider a single, optional, clearly-scoped escalation (a hosted reader like r.jina.ai as a fallback) — but treat that as violating the zero-services rule and require explicit opt-in.

## Two strongest rejected alternatives

1. **Neural HTML→markdown (ReaderLM-v2 / an LLM cleanup pass) — rejected.** It's the direction Jina moved and is seductive for a project that already has a local LLM. But it loses on every axis that matters here: on WCXB documentation it scores 0.776 F1 (below every top rule-based system *and* below bare Readability-family on docs), and it runs ~10,410 ms/page on an A100 — orders of magnitude over the ~3s target and infeasible on local CPU-class hardware alongside a 3B model. Rule-based extraction is 28–97 ms/page and better on your corpus. Neural extraction solves a problem (messy article-like pages) you mostly don't have.
2. **Adopting a full framework — colly for fetch, or Firecrawl/Jina as a service — rejected.** colly is a crawler built for bulk multi-page crawling with queues and callbacks; for single-page on-demand fetches it's pure overhead and the wrong abstraction. Firecrawl/Jina-as-a-service violate the zero-services, no-SaaS constraint, add network dependency and rate limits (keyless r.jina.ai is capped at 20 req/min), and — most tellingly — their *entire single-page core is Readability + a markdown converter*, which you can run in-process in <500 lines. Paying a service (or vendoring an AGPL TypeScript codebase) to do what two small Go libraries do locally is the opposite of "the 20% of Firecrawl that matters, in-process."

## Caveats
- WCXB is a 2026 benchmark maintained "as a hobby side project," and its top system (rs-trafilatura) was developed by the benchmark's own author and tuned against its dev set — treat the *exact* rs-trafilatura numbers with mild caution, though the held-out test set and the article-vs-documentation *pattern* are robust and corroborated by trafilatura's and Bevendorff et al.'s independent evaluations.
- Token counts are tokenizer-dependent (measurements use cl100k_base / GPT-4o-era encoding; your 3B model's tokenizer will differ, but ratios hold within ~±15%). Treat them as order-of-magnitude and measure on your own target pages.
- Latency figures for the headless path are estimates; cold-start Chromium cost varies with hardware and flags. Benchmark on the actual deployment before finalizing the ~3s budget.
- The `go-shiori/go-readability` archival (Dec 2025) means the most-cited Go Readability port is now unmaintained; the readeck fork is active but younger and less widely used — validate it against your golden set before committing.
- Anti-bot behavior is IP- and time-dependent; the same URL can return 200 from a residential IP and 403 from a datacenter IP. If Cerveau runs on users' local (residential) machines, block rates will be *lower* than the datacenter-scraper reports suggest.