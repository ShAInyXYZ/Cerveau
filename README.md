<div align="center">
  <img src="banner.svg" width="880" alt="Cerveau — local-first agentic coding harness"/>

  <!-- VERSION PILL — bump this on every release. It lives here, not in
       banner.svg, because a version baked into the SVG goes stale silently. -->
  <p>
    <img src="https://img.shields.io/badge/v0.6.0--alpha-LABRIG-C0304A?style=for-the-badge&labelColor=000000" alt="v0.6.0-alpha LABRIG"/>
  </p>

  <p><strong>A local-first coding harness for the models and hardware you own.</strong></p>

  <p>
    <img src="https://img.shields.io/badge/Go-1.25-C0304A?style=flat-square&labelColor=17140F&logo=go&logoColor=F2E1DE" alt="Go 1.25"/>
    <img src="https://img.shields.io/badge/Svelte-5-C0304A?style=flat-square&labelColor=17140F&logo=svelte&logoColor=F2E1DE" alt="Svelte 5"/>
    <img src="https://img.shields.io/badge/cores-llama.cpp_%2B_vLLM-C0304A?style=flat-square&labelColor=17140F" alt="Brain Cores: llama.cpp + vLLM"/>
    <img src="https://img.shields.io/badge/license-Apache--2.0-C0304A?style=flat-square&labelColor=17140F" alt="Apache-2.0"/>
    <img src="https://img.shields.io/badge/DCO-required-C0304A?style=flat-square&labelColor=17140F" alt="DCO"/>
    <img src="https://img.shields.io/badge/cloud-none-C0304A?style=flat-square&labelColor=17140F" alt="No cloud"/>
  </p>

  <p>Your models. Your machine. Your memory. No hosted Cerveau account required.</p>

  <p><a href="#quick-start">Quick start</a> · <a href="#tested-models-and-results">Tested models</a> · <a href="#patch-notes">Patch notes</a> · <a href="docs/README.md">Documentation</a> · <a href="https://cerveau.sh">cerveau.sh</a></p>
</div>

Cerveau connects your local model to a workspace, tools and persistent memory. A Go harness owns the execution loop, checks and recovery; an embedded Svelte panel and CLI let you follow the work and control each run. Connect a tool-capable llama.cpp or vLLM endpoint to get started.

## Patch notes

Version 0.6 is expanded below. Open an earlier release to read its changes in their original release context.

<details open>
<summary><b>🔬 v0.6.0-alpha — "LABRIG" · 2026-09-05</b></summary>

### v0.6: multi-GPU profiles and checked execution

<p>
  <img src="https://img.shields.io/badge/lab_rig-4×_RTX_3090-C0304A?style=flat-square&labelColor=17140F" alt="four RTX 3090 lab rig"/>
  <img src="https://img.shields.io/badge/cores-BF16_·_W8A16_%2B_MTP-E88BA0?style=flat-square&labelColor=17140F" alt="BF16 and W8A16 plus MTP profiles"/>
  <img src="https://img.shields.io/badge/runs-stepwise_%2B_verified-C0304A?style=flat-square&labelColor=17140F" alt="stepwise runs with verification"/>
</p>

LABRIG adds installable multi-GPU Core profiles and server-owned runs with step verification. The panel follows the worker's recorded state, and the single-card setup remains supported. Hardware configurations and measured outcomes are listed under [Tested models and results](#tested-models-and-results).

#### Core profiles

- **Unquantized weights across four GPUs.** Qwen3.8-27B has been tested with
  **BF16 weights and TP=4 across 4× RTX 3090s**. Here, unquantized means
  16-bit BF16 weights — **not FP32**. A separate **W8A16 + MTP** profile
  uses quantized weights and speculative decoding; the two configurations
  are distinct, not interchangeable benchmark results.
- **Profiles carry the configuration.** Installable BF16 and W8A16 TP=4
  profiles define the serving ports, systemd units, KV format and model
  window. Engine settings expose per-profile overrides and embedder
  placement; the BF16 profile places the embedder on the RTX 3060.
  The idle watchdog parks the loaded Core, and socket activation wakes it
  for the next request. See [Core profiles](deploy/profiles/README.md).
- **Engine patches are documented dependencies.** The rig's patched vLLM,
  including the prefix-cache/recurrent-state correction, has an
  [application and verification record](deploy/profiles/ENGINE-PATCHES.md).
  Rebuilding that environment requires reapplying and checking its patches;
  this harness release does not silently upgrade or retune the Core.

#### Execution and recovery

- **One owner per session and workspace.** The server accepts an identified
  run before execution. Retrying the same command does not start it twice,
  and closing the browser does not cancel it. Pause retains ownership;
  pause, resume, stop and steer target a specific run and control version,
  so a delayed click cannot change its replacement.
- **A plan needs a check, not just a checkbox.** Executable steps declare an
  `eval`, `command` or `contains` criterion. The supervisor records attempts
  and verdicts; file existence and prose claiming completion cannot mark a
  step passed. Older unchecked steps remain unverified. Revisions invalidate
  downstream results and require fresh checks after the correction.
- **Tools and context keep their boundaries.** Model calls are checked
  against the offered tools and JSON arguments; nested reflexes retain the
  session workspace, mode and guards. Replayed tool-call/result groups stay
  together, missing results remain unknown, and request admission includes
  tool schemas and output headroom. A re-check executes again rather than
  reusing an old success after the files change.
- **Thinking follows an explicit policy.** Off, low, medium and xhigh effort
  can be scoped to planning, every autopilot step or every turn, with bounded
  reasoning and overflow step-down. The harness probes the Core's serving
  window. Sampling includes a `default` preset that leaves the model's
  generation settings alone; effective settings are captured when a run starts.

#### Panel and run controls

- **Run state, plan state and evidence share a journal snapshot.** The chat
  strip and Planner use the server's step states. The UI distinguishes
  working, waiting for an answer, pause requested, paused and stopping;
  historical failures stay inspectable without becoming fresh error cards
  on every refresh. Rejected submissions retain the draft, and changing
  sessions cannot apply an old response to the new view.
- **Planner is built into the application.** Its versioned source stays in
  `rfx/planner`; executable and Planner upgrade or roll back together. The
  built-in copy takes precedence over installed copies without changing their
  files. The pack card and `/api/build` disclose the source and precedence;
  other RFX packs remain independently installed.
- **The matrix/status readout identifies the 0.6 alpha build.** Build
  metadata also exposes its source revision. Thinking and sampling controls
  share acknowledged settings, with future-run defaults separated from the
  active run's captured values. Per-reply working logs remain available
  after completion.
- **The work since 0.5 is included.** Per-session token usage records,
  copy/edit-and-resend, sanitized Markdown and inline-code fixes, bundled
  fonts, a collapsible project rail, Android welcome/reachability polish,
  `crvcli pair`, and a paired-device list with last-seen and revocation.
  Security changes include resolved-address SSRF checks, cross-origin
  mutation rejection, required download checksums and tighter token-file
  permissions. These remain a safety floor, not an OS sandbox.

#### Native tools and follow-up changes

| Date | Change | Technical record |
| --- | --- | --- |
| Sep 5 | Native DGV graph tools, GitHub 2.0 explicit-file staging, DevCheck browser evidence, and consented image attachments. | [Native RFX](docs/rfx-native-0.6.md) · [Conversion recipe](docs/rfx-dgv-conversion.md) |
| Sep 6 | Targeted reads with exact continuation, stale-source edit checks, and versioned evidence retained across retries. | [Targeted recovery](docs/targeted-file-recovery-2026-09-06.md) |
| Sep 6 | Browser diagnostics distinguish missing evidence, timeouts and process failures. Owned-server probes check delivery and file identity. | [Browser recovery](docs/browser-recovery-tools-2026-09-06.md) |
| Sep 7 | Native `browser_run`, `runtime_profile`, `run_checks` and `code_diagnostics` return bounded results with source and evidence references. | [Debugging tools](docs/native-debug-tools-2026-09-07.md) |

See the [0.6 release record](docs/release-0.6-LABRIG.md) for validation and the [model results](#tested-models-and-results) for recorded runtime and recovery tests.

</details>

<details>
<summary><b>🧠 v0.5 — "Cores" · 2026-08-19</b></summary>

### v0.5: swappable inference engines

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
  dense models with resident weights. These were the two deployment strategies
  tested for this release, not exclusive limits of either engine.
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

</details>

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
  error budget.
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
  nearest-match hints on a miss. These tools let the model inspect and edit
  specific regions instead of re-reading whole files.

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

**RFX design choices.** Reflexes integrate with the harness registry, keeping tool dispatch and permissions in one place:

| Concern | RFX approach |
| --- | --- |
| Context | Typed tool declarations instead of loading a prose skill for each action. |
| Arguments | Schema validation at dispatch; generated grammars where the runtime supports them. |
| Permissions | Capability cards and risk tiers enforced in Go. |
| Verification | Install-time contracts and explicit tool results. |
| Runtime | Composed steps, native handlers or subprocesses, depending on the pack. |
| Failures | Preserve stderr and return evidence for diagnosis. |

**Tooling:** `crvcli rfx` to list / show / install / remove / enable /
disable / test / distill — write your own reflexes in minutes, or convert
old prose skills with `crvcli rfx distill`. Prose skills keep working.

</details>

## Why a harness for local hardware

Local inference has practical constraints: finite GPU memory, context capacity and generation speed. Cerveau targets homelabs and workstations with consumer GPUs, including both mixture-of-experts (MoE) and dense models.

The approach is to put repeatable checks in the harness: validate tool calls, limit output to the available context, retain failure evidence, and verify work before advancing a plan. This helps a smaller model use its available capacity without relying on prompting alone.

### Using both GPU and system memory

With a MoE model, only a subset of experts is active for each token. llama.cpp's CPU expert offload lets you divide model storage between GPU memory and system RAM. Cerveau's original setup used this to leave GPU capacity available for other workloads. Dense-model tests used separate vLLM configurations, including four-GPU tensor parallelism.

## Tested models and results

These are recorded runs on the project's own hardware, not a standardized comparison between models. Runtime measurements and coding outcomes are listed separately.

### Model configurations

| Tested model | Approach | Recorded outcome |
| --- | --- | --- |
| **Qwen3.6-35B-A3B · Q4_K_M** | llama.cpp; one RTX 3090, a 16-core CPU and 128 GB RAM. Tune CPU expert offload with a 32K context. | Historical profile measurements: approximately **73–107 tokens/s**. The shared profile left about **12 GB of GPU memory** available. |
| **Qwen3.8-27B · W4A16** | Patched vLLM on one RTX 3090. Use an FP8 key/value (KV) cache and disable multi-token prediction (MTP) to fit a 96K serving window. | The 96K configuration started successfully. With more desktop GPU usage, a 90% allocation failed; reducing it to 88% allowed startup. |
| **Qwen3.8-27B · BF16** | Patched vLLM; tensor parallelism across four RTX 3090s, BF16 weights and KV cache, vision enabled, three MTP draft tokens. | September 4 rig checks recorded **92.2 tokens/s** for a 700-token decode and a correct read of a 1280×800 image. Thinking was off and sampling was greedy. |
| **Qwen3.8-27B · W8A16 + MTP** | Patched vLLM; four RTX 3090s, FP8 KV cache, prefix caching and three draft tokens. Test run controls in isolated workspaces with thinking off and Strict sampling. | Pause/resume, steering, interruption recovery, whole-plan and selected-step execution **passed** the [recorded acceptance cases](docs/release-0.6-LABRIG.md#real-w8a16-acceptance--post-release-follow-up). |

The model names above follow the project's recorded model and serving identifiers. The [Core profiles](deploy/profiles/README.md) and [engine-patch record](deploy/profiles/ENGINE-PATCHES.md) describe the four-GPU configurations. BF16 uses 16-bit weights; W4A16 and W8A16 are separate quantized configurations.

### Single-GPU offload measurements

The original Qwen3.6-35B-A3B measurements used Q4_K_M weights, a 32K context and these llama.cpp profiles:

| Profile | CPU MoE layers (`--n-cpu-moe`) | Reported speed | GPU memory left |
| --- | --- | --- | --- |
| `shared` | 34 | ~73 tokens/s | ~12 GB |
| `fast` | 16 | ~98 tokens/s | ~4 GB |
| `max` | 10 | ~107 tokens/s | ~1.4 GB |

These are historical profile measurements retained from the original README; raw benchmark receipts are not included in the repository. They do not measure the four-GPU setup or coding success.

### Coding and recovery outcomes

| Test | Approach | Outcome |
| --- | --- | --- |
| Regional repair with Qwen3.8-27B | Medium thinking, Default sampling, production context manager. Introduce a line-160 syntax defect, repair it, recheck the foundation and verify a continuation marker. | **3/3 steps passed**, one edit, six model requests, **24.294 seconds**. [Recorded result](docs/backlog-fixes-2026-09-07.md#verification). |
| Voxel-world generation and recovery with Qwen3.8-27B | Stepwise Autopilot with committed checks for storage, lighting, meshing, physics and browser behavior. | The copied-session recovery attempt **failed without an edit**; its repeat remained **unverified**. No end-to-end voxel pass was established. [Recorded results](docs/targeted-file-recovery-2026-09-06.md). |

## Context, recovery and memory

### Persistent sessions and bounded context

Each session has an append-only `events.jsonl` journal; the model's context window contains a selection of that history. Tool outputs are capped, and older evidence can be retrieved by event reference. Compaction preserves a briefing of the request, plan and recorded work rather than treating the remaining window as the whole session.

### Recovery from failed actions

Cerveau records failures and uses them to guide the next attempt:

- Failed commands return their **real stdout/stderr** to the model, so it reads
  the actual error and self-corrects (verified: it debugged its own Tailwind v4
  migration).
- Loop detection considers source changes and check results. Repeated reads or
  a different error message alone do not establish progress.
- Truncated tool calls trigger bounded retries with smaller actions. Replay
  validation keeps tool calls and results paired; missing results remain unknown.
- The agent knows its **workspace path, its reserved ports, and its model's
  actual modalities** — introspected at runtime, never assumed.

### Safety at tool dispatch

A dispatch guard pattern-matches tool *arguments* before execution and catches
the common footguns: `rm -rf /`, force-pushes, `DROP TABLE`, piping a remote
script into a shell. Destructive `mv` is auto-rewritten to copy-verify-delete.
The file tools (`read`/`write`/`edit`) are additionally jailed to the workspace —
lexically *and* through symlinks, enforced in Go, not prompted.

This is a **safety floor, not a general sandbox.** Ordinary shell calls run with your OS user's permissions; a pattern-based guard can miss obfuscated commands. Recovery and native checks use separate Bubblewrap restrictions. Run Cerveau as an unprivileged user and read [SECURITY.md](SECURITY.md) for the threat model.

### Five memory systems

| Memory | Store | Role |
|---|---|---|
| Working | the live window | what the model sees this turn |
| Episodic | `events.jsonl` | append-only source of truth, crash-safe |
| Semantic | Typesense (managed) | curated cross-session facts, deduped, with provenance |
| Codebase | SQLite graph | symbols and call edges for structural queries |
| Procedural | `~/.crv/skills/*.md` | markdown skills, loaded on trigger |

The harness selects relevant facts and past events for recall. Typesense supports searchable memory; the optional Nemotron embedder adds vector retrieval. When configured retrieval is unavailable, eligible paths can use lexical search or local journal evidence. See [memory and embedding conventions](docs/memory-embedding-conventions.md).

## Where Cerveau fits

Cerveau focuses on local model execution, checked plans and inspectable recovery. Its main design choices are:

- A Go harness with an embedded Svelte panel and a CLI using the same API.
- A model endpoint you operate, with separate llama.cpp and vLLM deployment profiles.
- Append-only session journals, bounded context and optional searchable memory.
- Native tools and RFX packs that share the harness's dispatch rules.

Choose it when you want to operate the model and inspect the execution loop on your own machine.

## Requirements

Cerveau targets Linux. Model weights and the inference engine are installed separately.

| Component | Requirement |
| --- | --- |
| **Go** | 1.25+ |
| **Node.js** | 24 recommended; supported older versions are 20.19+ and 22.12+. Used for the panel build and JavaScript tools. |
| **Brain Core** | A tool-capable llama.cpp or vLLM endpoint. |
| **Bubblewrap** | Working `bwrap` for JavaScript mutation checks, recovery isolation and native debugging tools. |
| **Python** | 3.10+, optional for the embedding sidecar. |
| **Browser tools** | Installed Playwright and Chromium; see [setup requirements](docs/GETTING_STARTED.md#optional-memory-and-debugging-dependencies). |

Typesense does not need a separate manual installation in the default setup: Cerveau downloads and manages an instance. Provision it and any optional dependencies before expecting offline operation.

> Keep the API bound to `127.0.0.1`. Ordinary shell tools run with your OS user's permissions. Read [SECURITY.md](SECURITY.md) before setting up remote access.

## Quick start

Build the panel first so Go can embed its output:

```bash
git clone https://github.com/ShAInyXYZ/Cerveau.git
cd Cerveau
npm --prefix panel ci
npm --prefix panel run build
mkdir -p build
go build -o build/crv ./cmd/crv
go build -o build/crvcli ./cmd/crvcli
```

Start your separately installed model server with tool calling enabled. Before launching Cerveau, create `~/.config/cerveau/config.json` using the [configuration example](#configuration), pointing at your model and workspace.

From the repository root, start the application:

```bash
./build/crv
```

Open [Cerveau on 127.0.0.1:7700](http://127.0.0.1:7700), choose your workspace and select a mode. For a first Autopilot task, ask it to create a small function, add assertions and run them.

The [Getting started guide](docs/GETTING_STARTED.md) covers model endpoints, optional memory services, browser dependencies and troubleshooting.

> **Reasoning models:** configure thinking scope and effort in the panel. The active run keeps its captured settings; changing defaults applies to future runs.

## The three modes

| Mode | Contract |
|---|---|
| **Discussion** | ultra-concise planning; writes limited to design artifacts; crystallizes into a committed plan |
| **Brainstorming** | deep research, web + code tools + memory, findings externalized to notes |
| **Autopilot** | executes a committed plan, checks results and attempts bounded repairs; pauses when it needs a decision or exhausts its budget |

Tool cards expose commands, results and evidence. Run controls let you pause, resume, stop or steer the active run. Failed steps retain their latest check and recovery history.

## Configuration

The configuration lives at `~/.config/cerveau/config.json`. Create it before a customized first launch; replace the workspace with an existing absolute path:

```json
{
  "project": "cerveau",
  "addr": "127.0.0.1:7700",
  "workspace": "/absolute/path/to/your/project",
  "model_ctx": 32768,
  "endpoints": {
    "model": "http://127.0.0.1:8080"
  }
}
```

Use your model server's base URL **without `/v1`**, and a context capacity it supports. Existing configuration files do not need to be overwritten.

Supported `CRV_*` variables override corresponding fields when an existing configuration is loaded; they are not all applied during first-time file creation. Session journals and managed memory live under `~/.crv/`; tools may also create evidence directories in the project. See [configuration and default endpoints](docs/GETTING_STARTED.md#2-configure-a-workspace-and-model-endpoint).

## Architecture

The panel and CLI share the Go API; model execution and memory services are separate processes.

```text
  Svelte panel ──HTTP──▶ Go core ──OpenAI API──▶ Brain Core (llama.cpp / vLLM)
   (go:embed)             │
                          ├─▶ events.jsonl        episodic — source of truth
                          ├─▶ Typesense (managed) recall index + semantic facts
                          ├─▶ SQLite code graph   symbols + call edges
                          └─▶ skills/             procedural, plain markdown
```

Tools declare their JSON schema, risk tier, mode availability and input limits in a shared registry. The Go executable embeds the panel and built-in Planner. Model runtimes, memory services and optional tool dependencies remain separate.

## CLI

The CLI uses the same local API as the panel. From the repository root:

```bash
./build/crvcli ask "explain the window manager"
./build/crvcli sessions
./build/crvcli health
```

Global flags precede the command, for example `crvcli -addr http://127.0.0.1:7700 health`.

## Development

After building the panel, run the local checks:

```bash
go test ./...
go vet ./...
(cd panel && npm exec -- vitest run)
```

For panel hot reload, run `npm --prefix panel run dev` alongside the Go API. Optional integration tests need their documented external prerequisites.

## Contributing

PRs welcome. Cerveau uses the **[DCO](https://developercertificate.org/)** — no
CLA, no signup, just sign your commits:

```bash
git commit -s -m "your change"
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Documentation

- [Getting started](docs/GETTING_STARTED.md)
- [Core profiles](deploy/profiles/README.md) and [engine patches](deploy/profiles/ENGINE-PATCHES.md)
- [Native debugging tools](docs/native-debug-tools-2026-09-07.md)
- [Memory and embedding conventions](docs/memory-embedding-conventions.md)
- [RFX implementation](docs/rfx-native-0.6.md) and [conversion recipe](docs/rfx-dgv-conversion.md)
- [0.6 release record](docs/release-0.6-LABRIG.md)
- [Documentation index](docs/README.md)

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
