# 0.6.0-alpha — LABRIG

Release preparation: 2026-09-05, `feat/stepwise-runs`. The original release,
based on `b570da2` plus working-tree changes, is checkpointed at `8571d5f`.
Follow-ups are separate commits below. This remains a **build-only** handoff:
no installation, service restart, push, Core switch or engine tuning is performed.
The generated `build.json` identifies the exact source/embedded-asset digest;
the historical base commit alone does not identify the final release.

## What this build changes

- One accepted owner per session and canonical workspace, including file
  leases across processes. Paused work retains its reservation. Chat, CLI HTTP
  entry points, step execution and manual RFX use scoped execution.
- Detached, idempotent start commands. Closing an HTTP observer does not
  cancel execution. Controls carry `run_id`, `control_id` and
  `control_version`; delayed controls cannot affect a successor or reverse a
  newer control. Question answers require both run and question identity.
- Validated plan criteria, plan-bound replay, persisted retry/revision
  counters and conservative sequential dependencies. Reopening a step
  invalidates downstream evidence before edits; revalidation follows the
  correction. File presence and model prose never manufacture a pass.
- One journal-prefix snapshot for messages, events, run, plan, report and
  current errors. Questions are copied while locked and scoped to that run.
  SSE carries resumable event IDs; the UI treats it as an invalidation hint
  and reloads the full canonical snapshot, with polling as a backstop.
- The Planner is versioned in `rfx/planner` (pack 1.5.0) and embedded in the
  host. Its built-in identity takes precedence over installed copies, which
  remain untouched. Other RFX packs remain external. Whole-plan and selected-step
  work each use one server command; no
  browser polling loop restarts interrupted work. Selections must include
  unfinished dependencies. An out-of-scope revision suspends for a decision
  instead of silently expanding the requested work.
- Shared tool validation and guards across chat, steps, verification and
  nested reflexes. Model arguments must be a JSON object; trusted internal
  no-argument calls retain their existing nil/null shorthand. Remediated
  arguments are revalidated and rechecked, without reusing approval for a
  changed denied action. Pre-edit backups use confined, exclusive file access.
- Context packing preserves tool-call/result groups, pins active intent,
  recounts after compaction and checks request/schema/output headroom before
  inference. Missing outcomes are explicitly unknown, not successful.
- Thinking/sampling defaults are acknowledged, persisted and serialized.
  An accepted run retains its settings. Requested/effective model settings,
  call budgets, verification evidence and terminal outcomes remain in the log.
- ChatUI retains rejected drafts and confirmed state on connection failure,
  rejects late cross-session responses, keeps plans inspectable, and shows
  actual run phases and controls. Current-run failures are separated from
  historical retries and optional background warnings.
- The checkpoint's compact dot matrix reads **V0.6** from the running app version;
  the full `0.6.0-alpha` identity remains in build metadata. Unknown
  health never falls back to an invented V0.3. `/api/build` and `crv -version`
  identify the build. The context meter's missing tooltip import is fixed.
- Upstream model errors preserve HTTP status and useful object/string/text
  diagnostics instead of turning authentication failures into JSON parse
  errors. Diagnostics are bounded and redact the configured model key.

The README also records the inherited profile, idle parking, thinking,
usage, Markdown, device management, Android and security changes since 0.5.
BF16 rig work means **unquantized BF16 weights with TP=4 across four RTX
3090s**, not FP32. W8A16 + MTP is a separate profile. Historical rig results
in `deploy/profiles/ENGINE-PATCHES.md` are not new benchmarks of this build.

## Validation evidence

| Check | Evidence / scope |
| --- | --- |
| Go regression suite | `go test -race -timeout 90s ./...`; also rerun by the release builder |
| Static checks | `go vet ./...`, `git diff --check`; builder stops on failure |
| Frontend | 58 Vitest tests after hardening; type checking has zero errors and 20 pre-existing warnings |
| Dependency check | Added Node type declarations for the Node-based bridge tests; patched transitive development dependency nanoid 3.3.16 → 3.3.18. `npm audit` reports zero known vulnerabilities |
| Control integration | Real HTTP admission, stale/duplicate controls, observer disconnect, reconnect and scoped question answers in `internal/api/runcontrol_test.go` |
| Selected execution | Dependency admission, one detached scope, failed checks and revision boundaries in `internal/loop/selected_test.go` |
| Recovery | Snapshot prefix parity and external leases; child process exits after an actual temporary-file effect but before its tool result. Replay remains interrupted/unknown and does not repeat the effect |
| Settings | Concurrent defaults/config saves, failed-save retention and active-run immutability in `internal/api/settings_test.go` |
| Browser | Production embedded panel + actual Go API/journal/SSE, with explicitly scripted downstream model. Desktop 1440×1000 and mobile 390×844: version, pause, reload/same owner, offline/reconnect, resume, stop, overflow and page-error checks |
| Real Core smoke | Existing W8A16 endpoint, isolated temporary session/workspace: three model calls, two tool-call/result pairs, write then read `LABRIG_OK`. 22.30 seconds. This is a tool smoke, not a throughput/thermal benchmark |

The first opt-in model smoke omitted the installed service's model name/key
and used a discussion-mode-disallowed `.txt` target. The final fixture uses
the permitted `.md` artifact and privately inherits the existing model
authentication/name. Neither failure was “fixed” by altering the Core.
Browser round one exposed the missing meter tooltip import; it was corrected
before the final confirmation, which passed both viewports with no page errors.
Previous development screenshots used mocked
API routes and are not the real-API acceptance evidence described here.

## Explicit alpha boundaries

This delivers the immediate release gates, not every extension in the
[broader design proposal](loop-contract-2026-09-05.md):

- Defaults apply to future runs, with the existing one-run sampling override.
  Dedicated session settings and active-run policy-update commands are not
  implemented or advertised by the UI.
- Sequential dependency invalidation is conservative; there is no arbitrary
  dependency graph or per-step filesystem ACL. Tool guards are not an OS
  sandbox, and generic shell commands can have effects beyond file tools.
- JSON syntax/object shape and tool-specific validation are enforced. A
  universal JSON-Schema validator and a fully normalized attempt/event schema
  remain future work.
- Incidents are a deterministic current-run projection, not a separate
  persisted `incident.opened`/`incident.resolved` event service. Historical
  failures remain in the journal. Transport resync reads a whole snapshot,
  not an incremental client event reducer.
- Restart preserves interruption and unknown effects; it does not promise
  exactly-once shell/network execution or silently resume a crashed worker.
  Inspect existing artifacts before requesting continuation. Legacy plans
  without criteria remain unverified and need a newly validated plan.
- Workspace selection failure rollback and multi-file Core-switch
  transactions remain legacy boundaries, not changes to the active rig.
- The real-Core smoke and bounded lifecycle cases below do not replace a full
  application benchmark, an extended thermal run, or exhaustive real-model
  revision/interruption scenarios.
  Existing unrelated Svelte warnings and the large-bundle warning remain.

## Post-release hardening

The original 0.6 tree is preserved by checkpoint commit `8571d5f`. Follow-ups
are separate commits; the operator installs. No installed binary, service or
Brain Core configuration is changed by this work.

1. **Incident chime restored.** Five new store tests failed before wiring the
   chime and pass afterward. Notifications and dismissal use session/run/event
   identity. Initial hydration, reload, session switches, repeated snapshots,
   reconnect and late cross-session responses are silent; genuinely new
   incidents sound once. Identical text in a later run is not hidden by an old
   dismissal. Frontend suite: 55 passing tests.
2. **Registry preparation consolidated.** Chat and plan execution share one
   preparation path and fail closed on enabled-reflex registration errors;
   partial registries never reach inference. Skills match the accepted current
   instruction, plus explicit original task context for plan execution only.
   Completed-task context does not leak into a new chat. The prepared bundle
   and load evidence are reused for the lifetime of the run. Removed the unused
   factory and its global-workspace closures. Tests first exposed factory-gated
   skills, divergent registration errors and uncached preparation; those tests
   now pass. The differing-workspaces skill read/write test is a regression
   guard: the prior chat implementation already used the session workspace.
3. **Dead report reconciliation removed.** Deleted unused `sameFiles`,
   `filesPresent` and `statOK`. Kept the existence-cannot-pass regressions and
   corrected inverted test names, comments and assertion messages. Report
   behavior remains exclusively derived from `ReducePlan`; targeted race tests
   pass both before and after this behavior-preserving cleanup.
4. **Planner ships atomically with Cerveau.** The canonical manifest and panel
   in `rfx/planner` are embedded, not copied to a second source directory.
   Production server and CLI prefer this built-in identity, including on fresh
   installs and when an old, malformed or renamed installed copy is present.
   Installed copies remain untouched; other RFX packs still load normally.
   The pack card discloses precedence and ignored copies. `/api/build` reports
   bundled version/content digest, precedence and actual loaded state.
   `crv -build-info` prints bundled identity before any configuration or service
   setup. The release builder derives metadata from that executable and no
   longer distributes a separate Planner archive.
   Validation: full Go race suite and vet, two packaging tests, 58 frontend
   tests, zero Svelte errors (20 existing warnings), and production panel build.
   Fresh-directory CLI checks identify the built-in supervisor without creating
   the external directory. Loaded bundled content digest:
   `464bc62b00112394383a98d88a60db8b99fdfe95a1c91a0bbad55a13eacf8893`.

## Real W8A16 acceptance — post-release follow-up

Product source: `c5d5cda` plus the opt-in acceptance fixtures committed with
this record. UI test build identity: `c5d5cda-live-acceptance`. Existing Core:
`http://127.0.0.1:18040`, served name `qwen3.8-27b`. Each case uses its own
retained session/workspace and read/write tools. Request settings are thinking
off / effort low / strict sampling in the isolated harness; the operator's
Core configuration and production defaults are unchanged. Runs are sequential.
Durations below are acceptance-case wall time, not throughput benchmarks.

Evidence root: `build/labrig-acceptance-NzUggG/` (retained, git-ignored).
It contains journals, request-message observations without authentication
headers, snapshots, marker files, test logs and browser screenshots.

| Case | Result | Recorded evidence |
| --- | --- | --- |
| Pause/resume | PASS | Run `d05abf5aab0bb8fe9080cbe7`; 64.21 s; 4 model requests, 2 tool pairs. Pause landed during `model_call`; confirmed paused owner stayed inactive for 600 ms; competing command rejected with HTTP 409; resume completed the same run. Control versions 1 → 2. Exact `pause.md` marker read back. `controls/pause/evidence.json` |
| Steer | PASS | Run `a1f0acced785ec0a67aeb1f4`; 59.37 s; 4 model requests, 2 tool pairs. Accepted during `model_call`; request 2 contained the exact steering instruction and its durable control receipt. Same owner completed; `steer.md` contains the changed marker `LABRIG_STEERED` plus newline, read back before completion. `controls/steer/evidence.json` |
| Interruption recovery | PASS | Run `9e8d202f88420dd41dc5de8c`; 63.24 s; 1 real model request, 1 tool call and no result. Disposable child exited 93 after the write in step 0, before recording its result. Reopen reports interrupted run and pending/unverified plan, not done. Three observer replays leave journal, marker hash and request count unchanged. `recovery/evidence.json` |
| Run whole plan | PASS | Actual embedded Planner button emitted one plan-bound `continue` command; browser closed after acceptance. Run `138476378209f67abfacf52e`; 31.574 s; 6 model calls, 4 tool pairs; both steps passed and exact marker contents checked. |
| Selected | PASS | Actual Planner checkboxes + Selected button emitted one command with `[0,1]`; browser closed after acceptance. Run `2f137b8440434ee9f33bffd5`; 76.846 s; 7 model calls, 4 tool pairs. States `passed/passed/pending`; third step has zero attempts and no artifact. |

Planner evidence is under `browser-retry-SEPByu/ui/`, particularly
`browser-evidence.json`, `whole-snapshot.json` and `selected-snapshot.json`.
Both sessions were reopened in a fresh browser context and displayed the
canonical final counts (2/2 and 2/3). No browser page errors were recorded in
the successful attempt. Desktop screenshots show built-in precedence and the
ignored 1.0.0 fixture; its before/after hashes match. Mobile overflow checks
also passed, but the existing Planner dock is desktop-only and the screenshots
retain the existing session-drawer presentation; this is not a mobile Planner
interaction or a comprehensive mobile visual audit.

The initial browser preflight remains under `ui/`: it submitted **no commands**.
It incorrectly waited for `V0.6 ALPHA`, although checkpoint `8571d5f` already
intentionally shortened the badge to `V0.6`. That assumption was removed from
the new acceptance script and corrected in the older scripted QA helper; the
product badge was not changed. The initial browser context also used a
service-worker-blocking option and reported sandboxed-frame service-worker
access errors. A fresh normal browser context produced no such errors. The
failed evidence was preserved, not overwritten. Before acceptance, the Core's
health request timed out and its service was observed starting/stopping;
successful model requests later established availability. No service action
was taken to obtain that result.

These are small harness-seeded plans with real inference, not model-generated
application plans, a repeat of the operator's NerdForSpeed benchmark, an
extended thermal test, or a proof of every possible interruption boundary.
Recovery also checks that `RepairToolGroups` marks the actual recorded call's
outcome unknown; it does not start a resumed model run after the crash and does
not claim that resumed inference consumed the repaired context.

After these live runs, the browser harness gained bounded API requests and an
exclusive evidence-file claim. Two local regression tests verify timeout and
preservation on attempted reuse; no additional Core runs were needed for those
harness-only guards. The product source exercised live is unchanged.

### Reproducing the opt-in cases

Use a fresh absolute evidence directory for `CERVEAU_ACCEPTANCE_ROOT` and set
`CERVEAU_ACCEPTANCE_MODEL_URL` to the existing local Core endpoint. Privately
inherit the operator's `CRV_MODEL_NAME` and `CRV_MODEL_KEY`; do not print them or
put credentials in logs. Without opt-in environment variables these tests skip.
Run the controls and recovery cases sequentially:

```bash
go test -race ./internal/api -run '^TestLABRIGRealCorePauseResume$' -count=1 -v -timeout 120s
go test -race ./internal/api -run '^TestLABRIGRealCoreSteer$' -count=1 -v -timeout 120s
go test -race ./internal/api -run '^TestLABRIGRealCoreRecovery$' -count=1 -v -timeout 120s
```

For the actual Planner buttons, also set `CERVEAU_ACCEPTANCE_UI=1` and start
`go test ./internal/server -run '^TestLABRIGRealCoreUIPreview$' -count=1 -v -timeout 16m`.
Once its isolated listener is ready on port 17707, run
`node scripts/qa-labrig-realcore.mjs` in another terminal with the same evidence
root. An existing browser runtime can be supplied through
`CERVEAU_PLAYWRIGHT_MODULE` and `CERVEAU_CHROMIUM`. The browser script stops only
the test listener; production port 7700 and service lifecycle are out of scope.
Existing evidence directories are rejected, never cleared for a retry.

## Build and handoff

```bash
node scripts/build-release.mjs
```

This runs frontend tests/type checks/build, Go race tests, vet and whitespace
checks; then builds `crv` and `crvcli` with the embedded Planner and writes
`build.json` plus `SHA256SUMS` under a fresh `build/cerveau-0.6.0-alpha-LABRIG-*`
directory. It verifies the binary's `-version` output without starting any
services. It never installs artifacts or touches `~/.crv/bin`.

The earlier scripted-model browser acceptance can be reproduced with the opt-in
`TestLABRIGUIPreview` in `internal/server/labrig_preview_test.go`, then
`node scripts/qa-labrig-ui.mjs`. That script accepts
`CERVEAU_PLAYWRIGHT_MODULE` and `CERVEAU_CHROMIUM` for an existing local browser
runtime. No npm dependency is installed implicitly. The preview uses temporary
data and port 17706, not the operator's 7700 service.

Deployment remains operator-only: inspect active runs, make and verify binary
backups, install the matching executables, restart only `cerveau.service`, and
verify `/api/build`, health and displayed version. Keep backups for rollback.
No separate Planner installation is needed; leave existing installed packs
untouched. Do not switch/reinstall the Brain Core.
