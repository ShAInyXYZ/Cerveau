<div align="center">
  <img src="banner.svg" width="880" alt="Cerveau — local-first agentic coding harness"/>

  <!-- VERSION PILL — bump this on every release. It lives here, not in
       banner.svg, because a version baked into the SVG goes stale silently. -->
  <p>
    <img src="https://img.shields.io/badge/v0.5.0--alpha-%22Cores%22-C0304A?style=for-the-badge&labelColor=000000" alt="v0.5.0-alpha Cores"/>
  </p>

  <p><strong>A local-first agentic coding harness — built from scratch to squeeze every drop out of the hardware you already own.</strong></p>

  <p>
    <img src="https://img.shields.io/badge/Go-1.25-C0304A?style=flat-square&labelColor=17140F&logo=go&logoColor=F2E1DE" alt="Go 1.25"/>
    <img src="https://img.shields.io/badge/Svelte-5-C0304A?style=flat-square&labelColor=17140F&logo=svelte&logoColor=F2E1DE" alt="Svelte 5"/>
    <img src="https://img.shields.io/badge/cores-llama.cpp_%2B_vLLM-C0304A?style=flat-square&labelColor=17140F" alt="Brain Cores: llama.cpp + vLLM"/>
    <img src="https://img.shields.io/badge/license-Apache--2.0-C0304A?style=flat-square&labelColor=17140F" alt="Apache-2.0"/>
    <img src="https://img.shields.io/badge/DCO-required-C0304A?style=flat-square&labelColor=17140F" alt="DCO"/>
    <img src="https://img.shields.io/badge/cloud-none-C0304A?style=flat-square&labelColor=17140F" alt="No cloud"/>
  </p>

  <p>Your models. Your machine. Your memory. No cloud, no accounts, no telemetry.</p>

  <p><a href="https://cerveau.sh"><strong>cerveau.sh</strong></a> · <a href="#quick-start"><strong>Quick start ↓</strong></a></p>
</div>

---

## 📌 Patch notes

### 🧠 v0.5 — "Cores" · 2026-08-19

<p>
  <img src="https://img.shields.io/badge/cores-llama.cpp_%2B_vLLM-C0304A?style=flat-square&labelColor=17140F" alt="two cores"/>
  <img src="https://img.shields.io/badge/context-96K-E88BA0?style=flat-square&labelColor=17140F" alt="96k context"/>
  <img src="https://img.shields.io/badge/verify-runtime_eval-C0304A?style=flat-square&labelColor=17140F" alt="runtime eval"/>
</p>

**Cerveau stops being a llama.cpp app.** The inference engine is now a
swappable part, and the model can check its own work at runtime instead of
guessing.

**Brain Cores**
- **The engine became a component.** A Core is a whole inference runtime —
  engine, quantisation, KV format, serving strategy — presented as one
  OpenAI-compatible endpoint. Cerveau picks a URL and nothing more, so it
  needs no knowledge of what runs behind it. **llama.cpp** stays the default
  Core. **vLLM** joins it as the second.
- **Two Cores, two different jobs.** llama.cpp offloads MoE experts to system
  RAM, which is nearly free when only ~3B of 35B parameters activate per
  token — that Core runs a large mixture-of-experts model on a 24 GB card.
  vLLM keeps weights resident and batches continuously, so that Core runs
  dense models with real throughput. Neither can do the other's job; that is
  why there are two.
- **Switching Cores keeps the session.** Context lives in Typesense and the
  embedder, not in the engine's KV cache, so a swap carries a briefing rather
  than a memory. Switch at task boundaries, never mid-turn.

**Making room for the Core**
- **The embedder moved to the CPU — it was failing on the GPU, not merely
  slow.** Sharing a 24 GB card left ~50 MiB free: a short string embedded in
  15 ms while a realistic batch of code returned HTTP 500. That surfaces as
  "memory never retrieves anything useful", not as an error anyone would
  notice. On CPU it costs ~297 ms per chunk — invisible inside a
  thirty-second turn — and returns 2.6 GB.
- **96K of context, bought by spending speed.** fp8 KV plus dropping
  speculative decoding took the cache from 40,329 to 179,443 tokens. Both
  were latency optimisations, and latency was never the metric: the
  mid-build 502 that used to end long runs now sits three times further away.

**The model can check its own work**
- **`check_page` runs JavaScript in the page and hands back the value.** It
  used to report console errors and whether an element existed — never the
  question actually being asked, which is whether the thing *behaves*. One
  benchmark run spent 26 of its 46 tool calls hunting for a browser driver
  this project has never shipped, because there was no other way to read
  runtime state.
- **A wall gets recognised as a wall.** Repeated failures are counted by
  shape rather than by command, so `playwright` and `playwright-core` register
  as the same dead end. The third one returns a question — is this installed,
  what do you already have — instead of a fourth error.
- **Churn is measured by whether the work moved.** The two most wasteful runs
  on record had almost no errors; one edited for four minutes while the file
  changed by a single byte. The harness now watches the workspace, not the
  error count, and says something. A nudge rather than a kill — an unchanging
  workspace is also what a finished task looks like.

**Context that degrades honestly**
- **Compaction hands over a briefing, not a gap.** Dropping the oldest turns
  silently made the model believe the session began later than it did, so it
  redid finished work. What replaces them is assembled from the log — original
  request, plan, completed steps, files on disk — and never written by the
  model, since asking it to summarise what it just lost is circular.
- **Exactly one system prompt, at position zero.** Strict chat templates
  reject a second system message even at the front of the conversation, which
  ended whole sessions mid-build. Recalled memory and skill notes now travel
  as user-role `<system-reminder>` text, escaped so a stored note cannot close
  the envelope and be read as something you typed.

**Panel**
- **A run started in the terminal is visible in the browser.** The panel could
  only see turns it started itself, so a CLI build rendered as a dead screen —
  made worse by assistant messages during a build carrying no text at all,
  because the model is working rather than talking. Live sessions now pulse in
  the rail and stream their tool calls wherever they began.

<details>
<summary><b>📱 v0.4 — "Pocket" · 2026-08-18</b></summary>

<p>
  <img src="https://img.shields.io/badge/phone-paired_%2B_biometric-C0304A?style=flat-square&labelColor=17140F" alt="phone access"/>
  <img src="https://img.shields.io/badge/identity-keystore_P--256-E88BA0?style=flat-square&labelColor=17140F" alt="device identity"/>
  <img src="https://img.shields.io/badge/panel-mobile_first-C0304A?style=flat-square&labelColor=17140F" alt="mobile panel"/>
</p>

**Cerveau in your pocket, over your own tailnet.** A native Android shell
reaches the harness from anywhere, and the panel it renders was rebuilt to
deserve the small screen.

**The phone**
- **Pair once, unlock forever after.** The desktop's ⌾ button mints a
  short-lived invitation — QR plus a 6-character code, one use, five
  minutes. The phone scans it **with its own camera** (a vendored,
  decode-only ZXing; the pairing payload is never handed to a third-party
  scanner) and registers a **P-256 key generated inside the Android
  Keystore**. The private key cannot leave the TEE, so copying the app's
  data to another phone yields an identity that cannot sign.
- **The token is sealed behind your fingerprint.** AES-GCM under a
  Keystore key bound to the device lock; opening the app asks for
  biometrics or your PIN, and a phone with no lock says so plainly rather
  than implying protection it cannot give.
- **The page never holds a credential.** A loopback bridge inside the app
  adds the bearer token and a fresh per-request device signature on the
  way out, so the WebView's JavaScript stays completely dumb.
- **Nothing about your network ships in the APK.** No hostname, no tailnet
  name, no machine IP — `strings` on the binary reveals none of it. The
  invitation carries the address, and the app refuses to even look for a
  gate until it has proven it is on your tailnet.

**The panel, rebuilt**
- **TypeScript, rune stores, real primitives.** A typed API client, an SSE
  stream that reconnects with backoff, `Chat.svelte` split from 592 lines
  into seven focused components, bits-ui dialogs with real focus traps, and
  the first frontend tests the project has ever had.
- **Mobile-first.** The session rail becomes a drawer under 900px, chrome
  hides on phones, and the plan strip shows five rows and scrolls the rest
  instead of swallowing the screen.
- **A new colour identity.** The panel moved off its old palette onto a
  near-black neutral base (`#09090B`) with a single warm crimson accent
  carrying every interactive and semantic signal. Greys were re-cut as a
  proper ladder — base, two surfaces, two divider weights, three text
  tiers — so depth now comes from flat steps and 1px hairlines instead of
  shadows and glow. Every colour is a token in `tokens.css`; no component
  hardcodes a hex.
- **Pick a workspace from the phone.** The desktop's native folder dialog
  opens on the *machine* — invisible from a phone — so narrow screens get
  an in-panel browser backed by a deliberately narrow endpoint: directory
  names only, nothing above `$HOME`, symlinks resolved before the
  containment check.

**Guards that tell the truth**
- Loopback requests are trusted again, so pairing a phone no longer locks
  you out of your own machine — while traffic proxied in from the tailnet
  is stamped and still fully gated.
- An existing token no longer refuses new devices; each registers its own
  key.
- Several error messages stopped asserting causes they never tested
  ("is tailscale up?", "wrong or expired code", "is it awake?"). An error
  now reports what was observed.

</details>

<details>
<summary><b>🧭 v0.3 — "Guidebook" · 2026-08-03</b></summary>

<p>
  <img src="https://img.shields.io/badge/self--repair-guidebook-C0304A?style=flat-square&labelColor=17140F" alt="guidebook"/>
  <img src="https://img.shields.io/badge/tools-serve_·_check__page_·_web__fetch-E88BA0?style=flat-square&labelColor=17140F" alt="new tools"/>
  <img src="https://img.shields.io/badge/guards-idle_based-C0304A?style=flat-square&labelColor=17140F" alt="idle guards"/>
</p>

**The harness stops failing on solved problems.** A week of building real
apps through Cerveau turned every failure into a structural fix — the
release's doctrine: *advice in a prompt is a suggestion the model may
ignore; a rule in the core always runs.*

- **The guidebook** — the core's book of self-fixes. A mechanical failure
  (busy port → next port, invalid regex → literal search) is repaired and
  retried by the registry itself, disclosed as `[auto-fixed] …`. Real
  errors still reach the model untouched. Add a rule = add a table entry.
- **`serve`** — long-lived static servers the agent can actually start
  (bash kills its whole process group on return, so `… &` servers died
  instantly). Start/stop/list, workspace-jailed, returns the URL.
- **`check_page`** — headless-browser feedback: console errors, uncaught
  exceptions, did-my-element-render (tag / `#id` / `.class`), software
  WebGL for Three.js apps. The model debugged and fixed its own broken
  game with it — CORS diagnosis to working canvas in one turn.
- **`web_fetch` v2** — the industry single-page pipeline in-process:
  Readability → markdown (code blocks + tables intact), outline-first for
  big pages with `section=`/`start_index` drill-down sized to the 32K
  window. Honest UA (a test fails if it ever impersonates a browser);
  404/bot-blocks return as *facts to route around*, never burning the
  error budget. Design record in `docs-private/WEBFETCH.md`.
- **Plans reach the plan card no matter what** — `commit_plan` accepts
  plain markdown (headings/lists/checkboxes become steps), and a plan
  written to a `.md` file is auto-committed as a structured plan event,
  disclosed. The Planner pack finally always has something to supervise.
- **Guards measure stuckness, not effort** — the turn timer is an idle
  timeout that resets on every tool result; token exhaustion checkpoints
  and continues (3 slices) instead of killing mid-build; `rm -rf` is
  judged against the *real* workspace boundary instead of "starts with
  /"; error messages that name a wall now also name the door
  (dev server → build once, serve the dist).
- **Targeted editing** — line-numbered reads, `from_line`/`to_line`
  ranges, indent-tolerant matching, deletion via empty `new_string`,
  nearest-match hints on a miss. The model lands edits on the first try
  instead of re-reading whole files.

</details>

<details>
<summary><b>🎛️ v0.2.1 — "RFX_UI" · 2026-08-02</b></summary>

<p>
  <img src="https://img.shields.io/badge/RFX__UI-tier_2-C0304A?style=flat-square&labelColor=17140F" alt="RFX_UI tier 2"/>
  <img src="https://img.shields.io/badge/panels-any_HTML%2FJS-E88BA0?style=flat-square&labelColor=17140F" alt="custom panels"/>
  <img src="https://img.shields.io/badge/capability-still_guarded-C0304A?style=flat-square&labelColor=17140F" alt="capability guarded"/>
</p>

**RFX_UI — packs now ship their own control panels into the chat.**
A Blender-style tab strip sits at the chat's right edge; each pack gets a
panel, two ways to build one:

- **Declarative widgets** (`ui:` in `pack.yaml`) — status metrics with
  semantic tones, buttons, fields, file lists, progress, toggles. Six
  lines of YAML, zero code, validated at load.
- **Full custom panels** (`ui/panel.html`) — *any* HTML/CSS/JS, the
  author's own design. Rendered in a sandboxed iframe (opaque origin,
  CSP: no network, no frames); its only door is the `window.rfx` bridge,
  and every call lands in the same guarded registry the model uses.
  **Presentation is free; capability is still RFX.**

The trust chain got real teeth on the way: guard denials are typed by
tier, the sensitive tier is satisfied by an explicit **user confirmation**
(host-owned confirm strips a panel cannot draw over — catastrophic is
never approvable, by anyone), and the workspace now follows the active
session, so panels always show the project you're actually in.

Built as the reference: a github cockpit pack — live status with per-file
line stats, a colored diff viewer, one-click stage/commit/push, repo
publishing via `gh`, commit-identity management, and a ✦ button that has
**the local model write your commit message**. Twelve talents, all YAML +
one HTML file, zero core changes — which is the point.

</details>

<details>
<summary><b>⚡ v0.2 — "RFX" · 2026-08-01</b></summary>

<p>
  <img src="https://img.shields.io/badge/RFX-v1_frozen-C0304A?style=flat-square&labelColor=17140F" alt="RFX v1 frozen"/>
  <img src="https://img.shields.io/badge/prose_in_context-0_tokens-C0304A?style=flat-square&labelColor=17140F" alt="0 prose tokens"/>
</p>

**RFX — the declarative capability stack — is how Cerveau grows new tools.**
A *reflex* is a single `.rfx.yaml` file: typed parameters compiled into the
model's grammar, steps that re-dispatch through the existing guard, a
permission card enforced in Go, and a fuzz contract verified at install.
Drop a file into `~/.crv/rfx/` — it's a native tool on the next turn;
group related reflexes into a *pack* (a folder with `pack.yaml`) and they
travel together.

**Why not MCP?** MCP was designed for frontier cloud models with giant
context windows. On a small local model with a 32K window, its costs land
exactly where it hurts:

| | MCP servers | RFX reflexes |
|---|---|---|
| **Context cost** | schemas + prose, always resident | name + GBNF grammar; zero prose |
| **Arguments** | free-form JSON, hope for the best | malformed calls impossible by grammar |
| **Permissions** | none in the protocol | capability card, enforced in Go |
| **Verification** | none — tools rot silently | fuzz contract at install, loud refusal |
| **Runtime** | a Node/Python process per server | composed steps or one-shot subprocess |
| **Failure style** | retry loops | real stderr kept, self-correction wired in |

**Tooling:** `crvcli rfx` to list / show / install / remove / enable /
disable / test / distill — write your own reflexes in minutes, or convert
old prose skills with `crvcli rfx distill`. Prose skills keep working.
*The brain deliberates — reflexes just fire.*

---

Cerveau is a coding agent designed as a **harness from first principles** — not a
chat UI with tools bolted on. A single Go binary owns the agent loop, the context
window, tool dispatch and structural safety guards; the Svelte control panel is
embedded inside it; the model is just a URL to your local `llama.cpp` server.

It exists for one audience: **people who run models on their own metal** —
homelabs, workstations, small servers — and refuse to let 90 % of their machine
sit idle while a cloud subscription does the thinking.

## Philosophy: the restrictions are the feature

Most agent harnesses are Swiss-army knives — and rightly so, for their goal:
they serve every provider, every model size, every deployment. But generality
has a quiet cost. Machinery built to work with *any* model ends up implicitly
designed around the *strongest* ones, and a small local model inherits
scaffolding that assumes it won't fail — then fails in ways nothing catches.

Cerveau makes the opposite trade. One target: **consumer-grade GPUs and
professional workstations, lots of system RAM, small MoE models.** Because the
target is fixed, every layer gets to assume it — output caps sized to what the
model can actually emit, grammars constraining what it generates, recovery
paths for the exact ways it breaks, a context discipline built for 32K rather
than pretending 200K. Nothing is provider-agnostic, and that's the point.

The result is not a smarter model — it's a harness that **absorbs the failure
modes of modest models**, so a few billion active parameters deliver work that
otherwise needs a much larger one. Specialization is what a general tool
cannot offer; it's the entire reason Cerveau exists.

</details>


## Why a harness for local hardware

Most agent frontends assume an infinite, fast, cloud-hosted model. Local reality
is different: your model is quantized, your VRAM is finite, your context window
is precious, and every token has a real cost in seconds. Cerveau is engineered
around exactly those constraints.

### 🧠 The whole machine, not just the GPU

Modern MoE models (Qwen3-A3B, Mixtral-class) are *made* for hybrid hardware:
only a few experts fire per token, so the hot path (attention, KV cache) lives
in VRAM while the bulk expert weights stream from ordinary system RAM.

The reference rig — one RTX 3090 (24 GB) + 16-core CPU + 128 GB DDR5 — runs a
**35B-parameter MoE at 73–107 tok/s with a full 32K context and up to 8K tokens
of output per call**. The same box, with the naive "everything the GPU can't
hold goes to CPU" defaults, ran at **0.9 tok/s**. That gap is pure
configuration, and the split is a *dial*, not a setting:

| Profile | `--n-cpu-moe` | Speed | VRAM left for other models |
|---|---|---|---|
| `shared` | 34 | ~73 tok/s | **~12 GB** — stack a TTS, draft model, image gen |
| `fast` | 16 | ~98 tok/s | ~4 GB |
| `max` | 10 | ~107 tok/s | ~1.4 GB — dedicated benchmark mode |

```bash
llama-server -m model.gguf --host 127.0.0.1 --port 8080 \
  -ngl 99 --n-cpu-moe 34 \    # split experts: hot layers in VRAM, rest in RAM
  -t 16 \                     # PHYSICAL cores — hyperthreads fight for bandwidth
  -c 32768 --cache-type-k q4_0 --cache-type-v q4_0 \
  --no-mmap --jinja
```

The all-in-VRAM configs people benchmark hit 110–140 tok/s — by giving the model
the *entire* GPU and a cramped context. Cerveau's split means your 64–128 GB of
RAM becomes model capacity, and your GPU stays *yours*: the `shared` profile
runs the 35B agent **and** leaves 12 GB for whatever else you stack.

### 📼 A context window treated like the scarce resource it is

Every session is an append-only `events.jsonl` — **memory is the state**, the
window is only a projection of it. Tool outputs are capped at dispatch and age
into event-pointers the model can re-pull on demand; the system prompt stays
KV-cache-stable across turns. On a 32K local context, that discipline is the
difference between an agent that works and one that forgets what it's doing.

### 🔁 Built for small models that make mistakes

A 3B-active local model is not Opus, and Cerveau doesn't pretend it is:

- Failed commands return their **real stdout/stderr** to the model, so it reads
  the actual error and self-corrects (verified: it debugged its own Tailwind v4
  migration).
- Loop detection is **result-aware** — re-running `npm run build` while fixing
  things is progress, not a loop; identical output twice is.
- Truncated tool calls are detected and fed back as "split the write" instead of
  dying — and a poisoned history can never break future turns (replayed calls
  are sanitized).
- The agent knows its **workspace path, its reserved ports, and its model's
  actual modalities** — introspected at runtime, never assumed.

### 🛡️ Safety that is structural, not prompted

A dispatch guard pattern-matches tool *arguments* before execution and catches
the common footguns: `rm -rf /`, force-pushes, `DROP TABLE`, piping a remote
script into a shell. Destructive `mv` is auto-rewritten to copy-verify-delete.
The file tools (`read`/`write`/`edit`) are additionally jailed to the workspace —
lexically *and* through symlinks, enforced in Go, not prompted.

This is a **safety floor, not a sandbox.** The guard is pattern-based: an
obfuscated command can evade it, and `bash` itself is not yet OS-sandboxed (a
Landlock jail is on the roadmap). Treat API access as shell access to your
machine — run Cerveau as an unprivileged user, and see [SECURITY.md](SECURITY.md)
for the full threat model.

### 🧩 Five memory systems, zero ceremony

| Memory | Store | Role |
|---|---|---|
| Working | the live window | what the model sees this turn |
| Episodic | `events.jsonl` | append-only source of truth, crash-safe |
| Semantic | Typesense (managed) | curated cross-session facts, deduped, with provenance |
| Codebase | SQLite graph | symbols + call edges, ~10× cheaper than grep for structure |
| Procedural | `~/.crv/skills/*.md` | markdown skills, loaded on trigger |

Recall is **system-owned**: relevant facts and past events are pulled into every
turn automatically. The agent never has to remember to remember.

## How Cerveau compares

There are excellent tools in this space, and Cerveau borrows no code from any of
them. Where they sit:

- **Chat UIs for local models** (Open WebUI, LM Studio, Jan) — great for
  conversation and RAG; they are not agentic harnesses. No autonomous tool
  loops, no plan execution, no structural guards.
- **IDE / terminal coding agents** (Aider, Cline, Continue) — strong coding
  agents, mostly designed around frontier cloud models, with the local case as
  a fallback.
- **Agent frameworks** (OpenHands, Goose) — powerful and general, typically
  heavier deployments and, again, tuned for models that rarely fail.

Cerveau's niche is narrower and deliberate: **an agent harness engineered
around how small local models actually fail.** Truncated tool calls, poisoned
histories, blind retries, hallucinated paths and ports — the recovery machinery
for those lives in the Go core, because a 3B-active model needs it on every
session. Add the event-sourced memory, the compiled-in safety guard, and the
single-binary deployment, and the result isn't a claim of novelty for its
parts — the loop, the tools, the vector store are all well-trodden ideas —
but of an ecosystem built coherently, from scratch, for one job: making modest
hardware do serious agentic work.

## Requirements

| | |
|---|---|
| **Go** | 1.25+ |
| **Node** | 20+ (build the panel once) |
| **llama.cpp** | a `llama-server` build + a GGUF model |
| **Python** | 3.10+ — optional, only for the embedder sidecar |

**Platform:** Linux (x86-64 / ARM64) today. macOS is close (core + syscalls work; the system monitor and folder picker need platform shims). A **Windows version is planned** — see the roadmap.

> **Security:** Cerveau binds to `127.0.0.1` by default. The API is
> unauthenticated and can run shell commands (autopilot) — do **not**
> expose it to a network without your own auth or tunnel in front.

Typesense is **not** a prerequisite — Cerveau downloads and manages its own
instance on first run, on its own port, without touching any existing install.

## Quick start

```bash
# 1. build the panel (embedded into the binary via go:embed)
cd panel && npm install && npm run build && cd ..

# 2. build the binary
go build -o ~/.local/bin/crv ./cmd/crv

# 3. serve a model (flags above). Reference model — the one all benchmarks
#    in this README were measured on: Qwen3.6-35B-A3B, Q4_K_M quant (~22 GB)
llama-server -m Qwen3.6-35B-A3B-UD-Q4_K_M.gguf --host 127.0.0.1 --port 8080 --jinja

# 4. run
crv
```

Open **http://localhost:7700**. Optionally start hybrid vector recall:

```bash
python3 sidecars/nemotron_embed.py   # OpenAI-compatible /v1/embeddings on :8081
```

> **Reasoning models:** Cerveau sends `enable_thinking: false` on every request —
> otherwise a thinking model burns its whole token budget inside `<think>` and
> returns nothing.

## The three modes

| Mode | Contract |
|---|---|
| **Discussion** | ultra-concise planning; writes limited to design artifacts; crystallizes into a committed plan |
| **Brainstorming** | deep research, web + code tools + memory, findings externalized to notes |
| **Autopilot** | full autonomy end-to-end; re-plans on failure; hands back only when truly blocked |

Live tool cards show the real command and its real output as it runs. Errors
surface as actionable cards with the reason expanded — never a silent spinner,
never a log wall.

## Configuration

Created on first run at `~/.config/cerveau/config.json`:

```json
{
  "addr": ":7700",
  "workspace": "/path/to/your/project",
  "model_ctx": 32768,
  "endpoints": {
    "model":     "http://localhost:8080",
    "embedder":  "http://localhost:8081",
    "typesense": "http://localhost:8189"
  }
}
```

Anything can be overridden via `CRV_*` environment variables. All runtime data
lives under `~/.crv/` — your project directories are never touched except by the
file edits you ask for.

## Architecture

```
  Svelte panel ──HTTP──▶ Go core ──OpenAI API──▶ llama.cpp (your hardware)
   (go:embed)             │
                          ├─▶ events.jsonl        episodic — source of truth
                          ├─▶ Typesense (managed) recall index + semantic facts
                          ├─▶ SQLite code graph   symbols + call edges
                          └─▶ skills/             procedural, plain markdown
```

Tools are declared once in a registry — JSON schema, risk tier, per-mode
availability, ingress cap — and their grammars are generated, never
hand-written. A release is a **single static binary**.

## CLI

```bash
go build -o ~/.local/bin/crvcli ./cmd/crvcli

crvcli ask "explain the window manager"   # one-shot, scriptable
crvcli sessions
crvcli health
```

## Development

```bash
go test ./...               # full Go suite
cd panel && npm run dev     # panel with HMR on :5171, proxying to :7700
```

## Contributing

PRs welcome. Cerveau uses the **[DCO](https://developercertificate.org/)** — no
CLA, no signup, just sign your commits:

```bash
git commit -s -m "your change"
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Status

**v0.1 — early, and daily-driven.** Cerveau builds real projects end to end
(scaffold → build → read its own errors → fix), and every number in this README
was measured, not estimated. It is still a young codebase: expect rough edges. Known limitations are
stated where they exist (e.g. per-session tool jailing is not yet enforced for
instant sessions) rather than papered over.

## License

**Apache-2.0** — see [LICENSE](LICENSE) and [NOTICE](NOTICE).

Use it, modify it, fork it, run it commercially — freely. One reservation, per
Apache 2.0 §6: **"Cerveau" is a trademark** of Mounir Belahbib and Shiny Studio
OÜ. Derivative products may not ship under the Cerveau name or branding
(no "Cerveau Pro"). Fork it proudly — under your own name.

---

<div align="center">
  <sub>Built by <a href="https://github.com/ShAInyXYZ">Mounir Belahbib (ShAInyXYZ)</a> · a <a href="https://cerveau.sh">Cerveau</a> project by <a href="https://shinystudio.xyz">Shiny Studio OÜ</a></sub>
</div>
