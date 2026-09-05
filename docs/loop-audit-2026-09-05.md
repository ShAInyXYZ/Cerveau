# Loop, tools, plans and ChatUI audit

Baseline: `feat/stepwise-runs`, commit `510df03`, 2026-09-05.
While this audit ran, the handover was committed as `b570da2`; its only change was `docs/HANDOVER-2026-09-05.md`. Audited application source remained identical to the baseline.
Scope: execution and observability contracts. Inference deployment and hardware tuning are outside this review.
Status: historical baseline audit complete. Implementation was subsequently
approved; the 0.6 LABRIG working-tree fixes and validation are tracked in
[the release record](release-0.6-LABRIG.md). Baseline line numbers and failing
probes below are preserved as historical evidence, not current test results.

The central defect is inconsistent ownership of truth: the conversational loop, supervised step loop, checkpoint replay, disk-derived report, panel store and RFX bridge each implement part of the lifecycle independently. The result is both false failure and false success. This audit does not attribute every regression to a particular historical commit; no historical bisect or live-model reproduction was performed.

Review in DGV: `http://127.0.0.1:7710/#loop-audit` (current issues) and `http://127.0.0.1:7710/#loop-contract` (proposed architecture). The companion contract is [loop-contract-2026-09-05.md](loop-contract-2026-09-05.md).

## Review method

Read DGV first, trace the implementation behind each edge, record an issue only with a concrete trigger and source evidence, then reproduce the important state transitions with a fake model and temporary workspaces. Existing passing tests establish a baseline, not that a proposed design is correct. Proposed changes remain open until agreed and demonstrated by acceptance tests.

DGV MCP is registered in Codex using Claude's existing launch command. A direct stdio MCP handshake confirmed DGV 0.2.0 and all 13 tools. The existing viewer at `http://127.0.0.1:7710` serves this repo's `dgv/` directory. The native tool catalog in the current conversation did not refresh, so this review uses a local MCP client against the same installed server. Registration follows https://learn.chatgpt.com/docs/extend/mcp?surface=cli.

The user subsequently supplied [HANDOVER-2026-09-05.md](HANDOVER-2026-09-05.md), which was read in full. It distinguishes live happy-path evidence from stub-only changes and explicitly says the native strip migration and versioning the installed planner pack remain open. Its historical 11/11 run is not evidence for pause, restart, revision ordering or multi-session correctness. Its operational restart/push/NFQ instructions are context, not authorization to perform those actions during this audit. The installed planner source was then read, producing L18 below. Current engine settings mentioned by the handover were not changed or revalidated by running inference.

## Findings ledger

### L01 — A session can have overlapping runs

Evidence: `internal/loop/run.go:45` unconditionally replaces the handle for a session; its deferred cleanup unconditionally deletes the entry. `internal/api/api.go:580`, `:639`, and `:851` admit execution without an atomic reservation. Two tabs or a repeated request can run the same session at once. The older run can erase the newer run's handle on exit; Pause/Kill and running indicators then lose their target. The panel's local `running` boolean is not a server-side guarantee.

Proposed contract: atomically admit one active run per session; use run IDs and compare-and-release ownership. Choose an explicit queue/reject policy for workspaces shared by sessions. Tests must cover overlapping starts, handle ownership and cancellation.

### L02 — Pause and Steer do not control supervised steps

Evidence: `internal/loop/run.go:62` and `:71` set `steered`/`paused`, but `internal/loop/autopilot.go:108` and `:267` never read those flags and never install `inFlight`. The step window is built from local items, so the steer message appended by `internal/api/api.go:759` is also absent from subsequent step requests. The API can acknowledge pause/steer while the step continues unchanged. Kill reaches the root context, but its terminal state has separate problems.

Proposed contract: a common runner consumes typed control commands at defined boundaries; publish requested and applied control events. Pause must have an observable paused state, and steer must reach the next model request without replaying completed side effects.

### L03 — New plans inherit checkpoints from older plans

Evidence: `internal/loop/steprun.go:69` replays every indexed checkpoint in the session against the latest plan. It neither starts at the selected plan event nor checks a plan ID. New plan step 0 can inherit old plan step 0's pass, even when its title and criterion changed.

Proposed contract: every checkpoint references immutable `plan_id`, `step_id`, revision and attempt. Legacy replay must at least stop at the latest plan boundary. A second plan in the same session must start clean.

### L04 — Supervisor recovery loses blocked and revision state

Evidence: `internal/loop/steprun.go:79` reads only index/status/rev/check/summary. A `failed` checkpoint always restores attempts to 1; `decision: blocked`, pair failure counts and target revision transitions are not restored. `internal/loop/autopilot.go:210` logs the requesting step's checkpoint, while `Supervisor.revise` changes an earlier target. Recovery cannot reproduce the in-memory cursor and limits.

Proposed contract: one deterministic event reducer for execution and UI, with explicit attempt/revision/blocked transitions. Replaying the log must equal the live state after every transition, including a restart during revision.

### L05 — Revision validation happens before the revision

Evidence: `internal/loop/autopilot.go:219` runs `dec.Reverify` immediately after the supervisor requests a revision; the earlier step is rewritten only on a later iteration. Checks can pass against old files and are not rerun after the edit. `internal/loop/steprun.go:145` also directly reopens passed steps without invalidating or rechecking downstream passes.

Proposed contract: invalidate affected evidence when a revision opens, execute the revision, then reverify dependent steps against the new workspace version. Manual and automatic revisions use the same transition path.

### L06 — A missing check can still earn a passed step

Evidence: `internal/loop/verifyrun.go:37` rejects a nil verify, but `internal/loop/autopilot.go:193` bypasses it and sets `Pass: !runFailed` for steps without checks. Prose/legacy plans can finish as passed with only a model summary. The word `unverified` in evidence does not change the passed status.

Proposed contract: separate evidence strength and outcome. Legacy plans require migration or visibly unverified completion; they must never silently acquire a verified pass.

### L07 — File existence manufactures UI completion

Evidence: `internal/loop/report.go:131` marks pending/partial steps done with the summary “verified on disk” if their declared files exist, even if their declared check is false or no run happened. Its shared-file exclusion requires identical file sets, not overlap. `filesPresent` at `:177` also searches unrelated one-level child directories. `internal/api/api.go:666` passes the global workspace, not the session workspace. `/report` and `/plan` can therefore disagree even without a replay bug.

Proposed contract: files are artifacts, not evidence of a criterion passing. Use exact session-relative paths, the same reducer as execution, and an explicit stale/unverified evidence state. Reproduced with an existing file containing the wrong value and no run: report said done.

### L08 — Live progress is missing, and plan hiding is not session-safe

Evidence: `internal/loop/autopilot.go:164` sets “running” only on a local results slice; no started event is persisted. `/plan` reports pending while the model is in flight. `internal/api/api.go:550` returns `episodic.State`, which has no `running` field, but `panel/src/lib/RfxCustomPanel.svelte:63` reads that field. Its session bridge consequently reports false. `panel/src/lib/stores/session.svelte.ts:318` polls no messages, and only polls reports for a local run or an existing report. A CLI/second-tab run can start and finish without the first plan or final reply loading. These are reproduced, except the bridge mismatch, which is directly confirmed by its payload contract.

`panel/src/lib/chat/PlanStrip.svelte:19` keys archive state by `plan_event_id`; `panel/src/lib/storage.ts:8` omits session ID. Event IDs such as `evt_000001` repeat across session files. Finishing one session's plan can hide another session's plan. Its completion timers are also not canceled on session changes and capture the reactive plan ID. The native strip is display-only, while the RFX planner has execution commands.

Proposed contract: all surfaces subscribe to the selected session, not only runs they initiated. Snapshot plus sequenced event replay includes phase, step and control state. Scope display preferences and timers to session + immutable plan version; retain accessible completed plans rather than hiding operational controls.

### L09 — Late responses and local booleans cross session boundaries

Evidence: `panel/src/lib/stores/session.svelte.ts:57` and the other load helpers read `activeId` before awaiting, then publish to global state without checking that the session is still selected. `select` at `:170` does not clear ticks, live steps, window data or local running state. A late session A response overwrites session B's messages. `send` at `:200` guards the local boolean, unlike the public `running` getter which also knows about CLI/other-tab runs. It sends a second run even when its own getter says running. Both cases are reproduced with the actual store.

Proposed contract: state keyed by session/run; request generation or cancellation guards every asynchronous publication. Admission is server-owned; UI disables conflicting commands using server state and handles admission rejection. Changing the selected session must not change the target of an in-flight operation or its completion callback.

### L10 — HTTP failures become null, then success

Evidence: `panel/src/lib/api.ts:43` maps every non-OK/network failure to null, dropping status and error body. `session.svelte.ts:240` plays “done” when chat returns null and the error list is empty. `:188` expects rewind to throw, but it returns null, so a refused rewind still sends replacement text. Settings optimistically change local values and catch exceptions that the helper swallowed. `RfxCustomPanel.svelte:113` spreads a null plan response into `{ok:true}`. Question answers clear locally without a confirmed acceptance. The HTTP-null, success sound and refused-rewind behaviors are reproduced.

Proposed contract: typed success/error transport preserving status, code, message, retryability and request/run IDs. Explicitly model not-found/empty versus disconnected/failed. Keep optimistic changes pending until acknowledged; never announce success or repeat an operation based on null.

### L11 — Error cards have no incident lifecycle

Evidence: `internal/api/api.go:674` returns all historical error payloads, without event/run identity or resolution state. `session.svelte.ts:89` filters every transient and takes the last three undismissed entries; old errors return on polling after a new send clears them. `ErrorCards.svelte:7` adds text-regex filtering and “retry” resends the last user text rather than the failed run/step command. A successful task can later acquire a red card from optional background distillation (`internal/loop/hooks.go:66`). Conversely, exhausted connection retries still use class transient (`internal/loop/loop.go:514`), so the panel suppresses a genuine terminal failure. These paths are source-confirmed, not a claim that every historic user card had this cause.

Proposed contract: separate retry progress, user action required, terminal failure, cancellation and background warnings. Incidents have IDs, scope, open/resolved status and precise supported actions. A resolved retry remains in the activity log; an exhausted retry produces one current actionable failure. Background memory failure does not change the task outcome.

### L12 — Tool capabilities and dispatch differ across paths

Evidence: `internal/loop/loop.go:260` layers RFX onto the session registry, but `:309` replaces it with `l.tools.WithSessionTools`, losing the session jail and RFX layer whenever a matched skill adds tools. The skill factory is global-workspace-bound (`cmd/crv/main.go:319`). `autopilot.go:281` uses only the base session registry, so the step handoff loses injected skills/RFX entirely. Manual RFX execution uses the global registry (`loop.go:191`), and its panel request carries no session ID. Selecting a session asynchronously repoints the global workspace (`session.svelte.ts:112`), coupling viewing and execution. The workspace registry cache at `cmd/crv/main.go:258` lacks the indexing/post-write hooks installed on the global registry; new per-workspace code databases can remain empty/stale unless separately indexed.

Separately, narrowing planning tool specs is not enforcement: `loop.go:709` dispatches any returned tool allowed by the mode, even when excluded from this call's planning capability set. A fake model returned an unoffered write, and the file was written. The step runner substitutes `{}` for malformed JSON (`autopilot.go:369`) and still dispatches; the chat runner rejects it. Mode/risk guards still exist—the defect is inconsistent additional phase/argument policy, not an absence of every guard.

Proposed contract: one immutable per-run capability bundle, composed from session workspace + base tools + approved skills/RFX. One dispatcher enforces run phase, offered capabilities, mode, argument validity, risk/approval and workspace on all direct/nested/manual calls. Selection is read-only. Index invalidation belongs to workspace services, not a selected global registry.

### L13 — Planning is optional in paths presented as stepwise

Evidence: `internal/loop/loop.go:236` resumes an unfinished plan before appending `msg.user`. The ChatUI SSE subscription waits for that exact new user event (`session.svelte.ts:218`), so the resumed run never unlocks live events. This is reproduced. If the old plan is already finished, `activePlan` remains nonnil and `planFirst` at `:343` is disabled for a new task. Missing commit_plan, repeated prose and rejected plans drop the gate (`:425`, `:597`, `:875`) and allow unplanned execution. Prose/markdown conversion also bypasses verifies; adding any markdown field exempts structured steps from validation (`internal/tools/plan.go:123`), reproduced.

Proposed contract: classify answer-only versus build/resume/revise explicitly; a build cannot silently downgrade from supervised execution to free-running. A failed plan becomes `planning_blocked` with bounded repair and a visible choice. Every accepted user command records an identity/boundary before work, and new tasks do not inherit a completed plan.

### L14 — ChatUI settings do not describe a reproducible run policy

Evidence: `/api/thinking` and `/api/sampling` are global (`internal/api/cores.go:82`, `:281`), but UI copy describes session defaults. Thinking persists, sampling does not. Changing thinking is advertised as affecting the next model call; execution snapshots it once per chat turn (`loop.go:335`) or step (`autopilot.go:316`). Planning reads it separately. Planning truncation tests `thinkLevel`, not the effective `callLevel` (`loop.go:547`): plan-only mode has execution off even while planning is reasoning, so its fallback takes the wrong branch. Planning uses a fixed 24,576 reasoning cap (`:896`) rather than effort-level caps; this is policy that needs to be exposed, not proof that the model ignored effort.

Automatic repeat recovery can override an explicitly chosen sampling preset with creative (`loop.go:471`). Per-turn sampling does carry through a chat-to-plan handoff, which is correct; direct `/plan/step` has no equivalent override field. Knobs fetch defaults once and maintain independent local state. `internal/llm/sampling.go:62` also mutates shared sampling without synchronization; the race risk is source-confirmed, not measured here.

Proposed contract: explicit global defaults, session overrides and next-run override; snapshot effective policy on run acceptance. A deliberate active-run update is a typed command applied at the next safe model-call boundary, with a settings-applied event. Log requested/effective effort, reason for fallback, reasoning/output budgets and sampling per call. Validate and acknowledge changes. No Core deployment changes are needed for this contract.

### L15 — Context compaction can miscount and break tool-call structure

Evidence: `internal/window/window.go:131` reduces total after demotion but retains original per-item counts; later trimming subtracts the original again (`:172`). A deterministic counter fixture reports -368 tokens for a 1,538-token output. Trimming works on individual messages, not assistant-call/result groups; another fixture leaves a tool result whose assistant call was removed. Last-six preservation plus a marker added after trimming does not guarantee the final request fits. Tool schemas/images and the effective output/reasoning reservation are not included in this manager's fit calculation. `loop.go:1060` stores a session-capturing resume closure on the shared manager; steps do not install their own brief. Their task/check messages are not pinned, and step tool results have no EvtID to enable pointer demotion.

Proposed contract: a per-run context builder with pinned task/step/constraints, atomic tool-call groups, exact recount after each transform, and final request admission including tools and reply budget. If mandatory context cannot fit, emit context-blocked before calling the model. No cross-session mutable resume closure. Arithmetic/tool-pair failures are reproduced; tokenizer accuracy and live engine limits need later integration tests.

### L16 — Freshness and “stuck” detection use unsafe assumptions

Evidence: `internal/loop/autopilot.go:127` snapshots planning reads once and reuses them for every step, including after edits; the rendered source block says not to reread (`:756`). `stepRunContext` carries selected check summaries, not the original task's complete constraints. Revision requests are extracted from English prose (`:642`), while the targeted step does not receive the full structured request. In the conversation loop, `guard.go:57` caches `check_page` by workspace fingerprint although eval/URL results depend on server/browser/time state. `loop.go:703` uses the fingerprint taken at iteration start: read → edit → same read in one tool batch can return the pre-edit cache. `guard.go:213` normalizes all digits, collapsing potentially meaningful numeric changes. These are source-confirmed risks; no live browser-result cache reproduction was run.

Proposed contract: version source observations and invalidate after every mutation, not just each model iteration. Cache only tools that declare a valid dependency/version key; browser/network probes default to uncached. Preserve meaningful argument/result differences. Carry a bounded task brief and structured revision request; make progress/limits depend on attempt state and observed changes rather than arbitrary prose.

### L17 — Terminal outcomes and evidence are incomplete

Evidence: `internal/loop/autopilot.go:246` maps untouched steps to skipped, even for deliberate single-step execution; its returned iterations are the number of plan steps, not actual calls. A max-runs exit can retain `handback:false` and return final_answer. `runStep` returns a no-tool reply before persisting its assistant usage/reasoning (`:365`). Verifications dispatch directly (`verifyrun.go:51`, `:78`) with only clipped checkpoint evidence, so the activity stream cannot show the full verification operation. Guard policy differs: steps use `newTurnGuard(0)` (four-minute idle default), bypassing the longer turn budget; direct step/autopilot HTTP handlers use 15 minutes while chat selects other durations. Cancellation, budget exhaustion, blocked steps and successful completion lack one shared terminal contract.

Proposed contract: separate run outcome from plan completion; single-step success can leave the plan pending, and budget exhaustion is suspended/blocked rather than completed. Record model/tool/verification start and terminal events with real counters. Use one documented deadline/retry policy across entry points. A stop must preserve artifacts and report which operations may have completed.

### L18 — Installed Planner still overrides the core and mislabels whole-plan execution

Evidence added after reading the handover: `/home/shiny/.crv/rfx/planner/ui/panel.html:121` folds all checkpoints by title and marks any step with existing files done. `:169` overlays only passed/failed/blocked core statuses; a core pending/running step never replaces the inferred done. Therefore the claimed “core cursor wins” is false even when `/plan` responds successfully. `nextTodo` at `:194` chooses the panel's inferred cursor, not `planState.next`. The “whole plan” branch at `:341` calls `rfx.runStep(-1,false)` once, which the core defines as one next step, not the remaining plan. With auto-continue off this does not execute the requested whole plan. Running protection also depends on the broken bridge running field from L08.

This installed pack is outside the repo and is identified as unversioned in the handover. Inspected SHA-256: `6873f2602cbc7784265e0561cb379d10e5cf1293f238dad6193ebd9ed8cc916f`. These are source-confirmed installed-code findings, not covered by the 22 repo contract probes. The pack was not edited. Proposed contract: version the pack with its bridge contract and parity tests; render every core step state exhaustively, remove disk-derived completion/cursors and give run_step and continue_plan distinct commands. Native and RFX surfaces must be tested against the same trace. A change to the repo alone cannot fix an independently deployed pack unless delivery/version negotiation covers it.

## Priority and evidence

First protect state and user work: L01–L05, L12 and L15. Then make the same state observable: L06–L11 and L17. Planning/context/settings (L13–L16) must be integrated into that lifecycle, not patched into a separate runner again. This order is an implementation dependency order, not a suggestion to leave false UI completion in production during a partial rollout.

Twenty-two failing contract assertions reproduce current behavior: 16 Go leaf checks and six frontend checks. They assert desired invariants, so **failure is the expected baseline result**, not a passing regression suite. Runtime probes use fake HTTP models and temporary workspaces. Frontend probes compile the actual store/client with the existing Svelte/Vite setup and mock network/audio; they are not browser visual tests.

| Finding | Reproduced behavior |
| --- | --- |
| L01 | Old run cleanup erases new owner |
| L02 / L08 | Accepted pause still makes next call; steer absent; active step reports pending |
| L03 | New plan inherits old pass |
| L04 | Blocked/attempt state and target revision fail replay equivalence |
| L05 | Manual and automatic revisions retain invalid downstream passes |
| L06 | Unchecked step passes; markdown field bypasses structured validation |
| L07 | Existing wrong file marks an unexecuted step done |
| L08 | External run polls neither first report nor messages |
| L09 | Late session A overwrites B; server-running state does not prevent send |
| L10 | HTTP 409 becomes null; null chat announces success; refused rewind still sends |
| L12 | Unoffered planning write executes |
| L13 | Resume omits the user event required by the UI stream gate |
| L15 | Negative/mismatched token count; orphaned tool result after trim |

Reproduction sources and commands: [loop-audit-probes/README.md](loop-audit-probes/README.md). Source-confirmed findings without a probe are labeled above and need acceptance coverage during implementation. Other behavior, including restart safety around real shell/browser side effects, still needs integration validation.

## Verification record

Final checks on 2026-09-05, after reading the handover:

| Check | Result |
| --- | --- |
| `go test ./...` | Passed; existing suite, cached results |
| Normal panel `vitest run` | 34 tests passed across six files |
| Audit Go overlay | 16 intended failing leaf assertions, fake model/temp workspace only |
| Audit frontend config | Six intended failing assertions against actual store/client with mocked I/O |
| `go vet ./...` | Existing unreachable code, `internal/loop/autopilot.go:403` |
| `svelte-check --threshold error` | Existing eight errors and 21 warnings; errors in Turns, SamplingKnob, ThinkingKnob |
| Panel production build | Passed earlier in this review; application source unchanged since |
| DGV `loop-contract` / `run-lifecycle` | Zero errors, warnings or info diagnostics |
| DGV `loop-audit` | Zero errors; 18 deliberately unresolved issue warnings; two informational notes on current topology/protocol |
| DGV `run-stepwise` | Zero errors; one new unresolved audit flag and its pre-existing acknowledged info |
| Viewer / MCP | Viewer serves this repo at :7710; registered stdio server handshake and tool calls succeeded |
| Worktree scope | No changes to `internal/`, `cmd/` or `panel/` versus audited source; handover-only commit preserved |

No inference workloads or Core settings were changed by the review. Diagram validation was structural; no browser visual acceptance or real-model behavior is claimed for the proposed design.

Review artifacts only were added. No application source, inference settings, live sessions or user workspaces were modified. The DGV audit marks unresolved defects; the target graph marks proposed components as todo, not implemented or proven. Lint validates diagram structure, not runtime correctness.
