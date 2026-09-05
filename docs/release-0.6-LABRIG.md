# 0.6.0-alpha — LABRIG

Release preparation: 2026-09-05, `feat/stepwise-runs`, based on `b570da2` plus
the working-tree changes. This is a **build-only** handoff: no installation,
service restart, git commit/push, Core switch or engine tuning is performed.
The generated `build.json` identifies the exact source/embedded-asset digest;
the base commit alone does not identify this uncommitted release.

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
- The Planner is versioned in `rfx/planner` (pack 1.5.0) and packaged with the
  host. Whole-plan and selected-step work each use one server command; no
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
- The dot matrix reads **V0.6 ALPHA** from the running app version; unknown
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
| Frontend | 50 Vitest tests; type checking has zero errors and 20 pre-existing warnings |
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
- The real-Core smoke does not replace a full application benchmark, an
  extended thermal run, or exhaustive real-model revision/pause scenarios.
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

## Build and handoff

```bash
node scripts/build-release.mjs
```

This runs frontend tests/type checks/build, Go race tests, vet and whitespace
checks; then builds `crv` and `crvcli`, packages the Planner and writes
`build.json` plus `SHA256SUMS` under a fresh `build/cerveau-0.6.0-alpha-LABRIG-*`
directory. It verifies the binary's `-version` output without starting any
services. It never installs artifacts or touches `~/.crv/bin`.

Browser acceptance can be reproduced with the opt-in
`TestLABRIGUIPreview` in `internal/server/labrig_preview_test.go`, then
`node scripts/qa-labrig-ui.mjs`. That script accepts
`CERVEAU_PLAYWRIGHT_MODULE` and `CERVEAU_CHROMIUM` for an existing local browser
runtime. No npm dependency is installed implicitly. The preview uses temporary
data and port 17706, not the operator's 7700 service.

Deployment remains a separate action: inspect active runs, make and verify
binary/Planner backups, install both matching artifacts, restart only
`cerveau.service`, verify `/api/build`, health and displayed version, and keep
the backups available for rollback. Do not switch/reinstall the Brain Core.
