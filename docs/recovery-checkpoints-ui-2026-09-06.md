# Checked recovery progress and quieter UI — 2026-09-06

## Observed failure

The latest Minecraft recovery (`ca8ac7ac2e132607d9ee1bb3`, original journal read-only, 05:32:49–06:11:12 UTC) exhausted two attempts. It made 53 model requests including four output-only retries, and 58 tool calls. The displayed 32 is the action-bearing model-round cap of the final attempt, not 32 executions of one operation. Its final attempt recorded 14 structured repair calls; a call count does not prove 14 successful repairs.

Attempt 1 (`evt_001834`) made 19 model requests, 23 tool calls and four structured repair calls (two failed as tools). Attempt 2 (`evt_002023`) made 34 model requests, 35 tool calls and 14 structured repair calls (all tool-level successes). The final authoritative verification (`evt_002327`) returned `sky=0, block=0`; earlier observations included `sky=15, block=0` and `sky=15, block=15`. None satisfy the unchanged assertion `sky=15, block=14`.

The earlier extension policy treated changed workspace bytes as sufficient reason to grant more rounds. Comment changes or ineffective repairs could therefore buy more execution while the committed lighting assertion continued to fail. The report also mixed an earlier tool error with the fresh final verification failure. These are harness issues separate from the generated voxel implementation's lighting defect.

## Implementation contract

- Recovery first checks the unchanged committed assertion against the current workspace. A passing baseline can avoid unnecessary repair unless explicit steering or revision still requires work.
- Check a repair batch after two source-change observations, four model rounds with unchecked changed source, or an attempt boundary, at the next completed tool-group boundary. Return fresh, identified evidence before further repairs and ask for a specific hypothesis, source evidence and predicted check result.
- Three failed repair/check cycles hand back for a decision, including changed outputs that still fail. Preserve the existing total time, token and round guards; bounded recovery stops do not automatically receive a second fresh attempt budget.
- Repair-based round extensions require changed declared source plus a newly observed check outcome. Unrelated artifact writes, file touches and repeated failures are not sufficient. Novel output is explicitly not proof of improvement, and it does not evade the three-cycle bound. The existing one-time pre-repair inspection continuation remains: at least 1,024 newly inspected current-source bytes may use one existing eight-round extension slot before any failed repair cycle; repeated/stale ranges do not qualify.
- Keep fresh verification evidence separate from historical execution/budget-stop context. Resume continues to target the canonical blocked step, recheck affected prerequisites and advance only on passing checks.
- Recovery now invalidates passed shared-file evidence on both sides of the repaired step, not only earlier steps. A lighting repair also rechecks later passed meshing when both depend on `world.js`; unrelated files, the active step and unstarted steps are excluded. Selected runs leave unselected affected passes awaiting recheck without executing their checks. Explicit revisions retain their separately scheduled downstream rechecks.
- Preserve the original benchmark files, session and committed criteria. Model repair success is not guaranteed by these harness changes.

## UI scope and protected behavior

The private backlog authorizes these refinements of the existing interface, not a new visual identity:

- Quiet project/session navigation: flat inactive rows, visible selection and focus; preserve grouping, selection, create, rename, delete and instant sessions.
- Repeated successful reads: expandable consecutive same-file groups in live and historical activity, with original identities, ranges, arguments, results and chronology retained. Notes, failures, edits and meaningful state changes remain boundaries. Grouping is a presentation change, not a reduction in model/tool execution.
- Compact incident presentation: named blocked step and recovery explanation first, exact technical evidence in a disclosure, existing recover/continue and dismissal semantics retained. Manual Reflex incidents do not become plan retries.
- Suspended status uses the blocked prerequisite, not the last step executed. Running status still shows the actual execution cursor.
- Long-prompt composer: bounded multiline shape, full-width text above its action row, original draft/attachment/IME behavior preserved. Narrow screens give the input its own row.
- Stream-to-dock transition: an inset fading hairline outside the scrollable content, no overlay hiding the last message and no new animation.

Incumbent tokens, colors and typography remain the visual authority because no root DESIGN.md exists. Impeccable's quieter/operate guidance informed the hierarchy; no design-system initialization or external assets were introduced.

## Verification

- Final `node scripts/build-release.mjs` — PASS: 101 Vitest tests; full `go test -race -timeout 90s ./...` including all 203 loop tests; `go vet ./...`; `git diff --check`; Svelte checking with zero errors/eight existing warnings; production assets, binaries and checksums. The existing bundle-size warning is unchanged.
- Eight new recovery-cycle regression tests passed under `go test -race ./internal/loop -count=1`, with `go vet ./internal/loop` passing. Three additional shared-source regressions were then added tests-first: all failed against the previous implementation and pass after symmetric invalidation. The key regression repairs step 2, breaks previously passed step 3, verifies step 3 becomes the canonical blocker and proves step 4 is not executed. Other coverage preserves selected scope and excludes unrelated files.
- Browser fixture runner: `CERVEAU_PLAYWRIGHT_MODULE=/home/shiny/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright/index.mjs node scripts/qa-recovery-ui.mjs`.
- Evidence: `build/recovery-ui-CNATJJ/results.json` and adjacent screenshots. Desktop (1440 px), mobile (390 px), narrow (320 px) and 200% CSS zoom pass recovery targeting, recovery phases, stale-pass labels, full benchmark composer, live/historical grouped reads, retained failure rows, technical disclosures, idle transitions, no horizontal overflow and no page/console errors. CSS zoom is not a substitute for every native browser zoom mode.
- Two bounded browser review rounds. The first revealed a zero-height multiline textarea and ResizeObserver delivery errors; the final round verifies the corrected flex sizing and deferred width measurement. Screenshots were visually inspected, not just existence-checked.
- Targeted Impeccable detector: no findings in the edited chat components; sidebar checked separately. Existing project warnings are not silently treated as fixed.
- These browser checks use the actual Svelte UI with a mock API, not live Core inference. Original Minecraft completion and live repair success remain **unverified**; no production run has been resumed by this task.

## Delivery

Initial full release gates passed (101 Vitest tests, full Go race suite, `go vet`, Svelte checking with zero errors/eight existing warnings, production assets and binary checksums). Bundle `cerveau-0.6.0-alpha-LABRIG-Z4q965` was prepared but **not installed**: final integration review found the downstream shared-file recheck gap described above. It and preparation `install-backup-harness-recovery-Yi6akB` are marked do-not-install. No service was stopped.

### Installed corrected release

- Bundle: `build/cerveau-0.6.0-alpha-LABRIG-4J9ORO`.
- Version/revision: `0.6.0-alpha LABRIG`, `a3a8367-dirty-45e4ab58c05d`, confirmed live at `http://localhost:7700/api/build`.
- Installation: 2026-09-06, 08:40 CEST. `/api/sessions` confirmed no running sessions immediately before stopping only `cerveau.service`. No benchmark retry was submitted.
- Verified backup and installation receipt: `/home/shiny/.crv/install-backup-harness-recovery-XPmOAD/install.json`. Only `crv` and `crvcli` were replaced after verified copies; prior binaries remain recoverable in `old/` and `retired/`. Earlier preparation `Yi6akB` was never installed.
- Harness restarted with PID 2251434. Selected W8A16 Core PID 2139642 and embedder PID 476517 stayed unchanged and active/running. No Core lifecycle commands, model changes or tuning were performed.
- `/api/health`: model, embedder and Typesense healthy. Startup tool-call canary passed. `/api/rfx`: 21 reflexes, no errors. `/api/idle`: active. No production session running after installation.
- Installer verification confirms original Minecraft `world.js` and session journal, configuration, service definitions, installed packs/helpers and sampling/thinking settings unchanged.
- Served HTML, JS (`index-PxFDABQ9.js`) and CSS (`index-DkPfXu7f.css`) bytes match the packaged assets.
- This receipt and final DGV delivery notes were recorded after the build. They do not represent another binary change. Full voxel completion remains unverified; operator can refresh and choose **Recover step and continue plan**.
