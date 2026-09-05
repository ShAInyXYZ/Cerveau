# Proposed loop contract

Status: broader design proposal with a 0.6 LABRIG implementation slice. The
user approved implementation and a build, not production cutover. See the
[release record](release-0.6-LABRIG.md) for delivered behavior, actual evidence
and explicit alpha boundaries; proposed session/active-run settings,
normalized incident events and generic schema validation are not implemented.
Baseline: [audit](loop-audit-2026-09-05.md). DGV: `loop-contract` and
`run-lifecycle`. The working inference stack remains unchanged.

0.6 decisions: reject conflicting session/workspace starts; retain leases
while paused/waiting; refuse unchecked legacy steps; use sequential dependency
invalidation, bounded retry/revision counts, future-run defaults and one-run
sampling overrides. HTTP controls additionally require run identity and an
idempotent control ID/version. The UI resnapshots one journal prefix after SSE
invalidations and polling; it does not implement the incremental reducer
described as a possible extension below.

## Invariants

1. One accepted active run owns a session. Starting from another surface cannot create a second owner or overwrite its control handle.
2. A build uses a validated, versioned plan. Answer-only requests use the same runner without a plan; a planning failure never silently becomes unrestricted execution.
3. A pass means a declared check passed against a known workspace version. Files existing and the model saying “done” are not equivalent evidence.
4. Execution, recovery, ChatUI and RFX derive state from the same ordered events. No separate filesystem-based completion calculation.
5. Every accepted control has a visible requested/applied/rejected result. Pause is not displayed as applied while tools continue.
6. Every side effect has a recorded start and known or explicitly unknown outcome. Transport retries never imply permission to repeat a write.
7. A terminal run outcome is explicit and separate from whether its whole plan is complete. Quiet UI recovery cannot turn an actual failure into success.
8. Per-call capability, settings and context versions are inspectable. No UI-only state decides where a tool writes or whether a run succeeded.

## Ownership and commands

All chat, CLI and RFX execution enters one command service. Proposed command envelope:

```text
client_request_id, session_id, command, expected_state_version,
target_run_id?, target_plan_id?, target_step_id?, payload
```

Commands are start_task, continue_plan, run_step, revise_step, pause, resume, steer, cancel, answer_question and update_run_settings. The server validates authority, target identity, current state and payload. Repeated client_request_id returns the original acknowledgement/result. A stale step/revision button is a typed conflict, never silently applied to the newest plan.

Admission serializes on session, records run.accepted durably and returns 202 with run_id and state version. Different concurrent starts return 409 with the existing owner; initial implementation need not add a hidden queue. Execution belongs to a server worker context, not the lifetime of the initiating HTTP connection. Closing/reloading a tab disconnects an observer; Cancel is the explicit execution command.

Use a canonical workspace identity and one write lease across sessions, not just per session. Proposed first version rejects a conflicting build with “workspace in use by …”; answer-only/read-only work may proceed with versioned observations. A paused/waiting build retains its reservation until explicitly suspended/cancelled, making this visible. If we choose to release on pause instead, reacquisition must invalidate/recheck affected evidence before resume. The lease policy is a review decision, not an implicit implementation assumption.

An in-process registry can track cancellation handles, but it cannot be the only truth. Compare-and-release by run ID. On process restart, reconstruct durable state; unfinished calls become interrupted/unknown, not idle success. A second Cerveau process must not write the same session log unnoticed: use a process/file lock or equivalent shared storage coordination.

## State model

Keep dimensions separate to avoid contradictory combined states:

| Dimension | Values / meaning |
| --- | --- |
| Run status | running, pause_requested, paused, waiting_user, suspended, completed, failed, cancelled, interrupted |
| Active phase | classifying, planning, model_call, tool_call, verifying, reverifying, retry_wait; absent when inactive |
| Plan status | draft, ready, in_progress, blocked, complete, superseded |
| Step status | pending, running, verifying, passed, needs_reverify, blocked, waived |
| Attempt outcome | succeeded, check_failed, tool_failed, model_failed, cancelled, interrupted, budget_exhausted |
| Evidence | verified / unverified / stale; check identity + workspace/input version + observation |

Run “completed” means its requested unit completed: a run_step may complete with the plan still in_progress. The UI must say “Step 2 passed; 3 steps remain”, not “Plan complete.” A waiver is a deliberate user decision with reason and remains visibly unverified; it does not count as all checks passed. Retryable failed attempts stay in history while the step is pending/running again. Blocked means automatic work has stopped and needs a specific decision.

Normal build: classify → bounded planning → validate/commit → select runnable step → model/tool cycle → declared verify → next step or finish. A single-step request stops after its requested attempt/check under the same supervisor; no second executor implementation. A read-only answer skips plan selection but shares model/tool dispatch, controls, context packing, events and outcomes.

### Controls

| Command | Contract |
| --- | --- |
| Pause | Record requested immediately. Interrupt an in-flight model call; stop scheduling additional tools. Let a non-interruptible side effect settle and journal its outcome. Publish paused only at that safe boundary. |
| Resume | Validate owner, workspace/evidence version and remaining budget; continue the same run/cursor without repeating settled tools. |
| Steer | Append the user's message and typed steering event, interrupt/rebuild the next model request, preserve settled tool results. If it changes task scope or acceptance criteria, explicitly revise/replan rather than silently mutate the current check. |
| Cancel | Stop scheduling; request cancellation of supported calls/process groups; settle or mark unknown active effects. Publish cancelled only when the worker has stopped. Cancellation does not roll back completed files. |
| Answer | Requires question_id and run_id. Clear the prompt only after accepted; expired/already answered returns a typed response. A tool waiting for the user is waiting_user, not a model stall. |
| Settings update | Apply a validated policy version at a safe call boundary; show pending until settings.applied. No change to an already submitted model request. |

Paused/waiting time does not consume the active-progress idle budget. Cancellation must preempt retry backoff. The UI can distinguish “Pausing after current tool” from “Paused”; if termination cannot be confirmed, it says so instead of hiding the activity indicator.

## Planning, revision and evidence

The immutable task brief includes user requirements, constraints, workspace, attachment references and requested deliverable. A step has stable step_id, detail, declared outputs/write scope, dependencies and a criterion that is valid for the available tools/environment. The plan has plan_id/version and references its task brief. Step display indices are never identity.

For the first implementation, sequential dependency order is the conservative default; a later step depends on earlier work unless the plan explicitly and validly says otherwise. File overlap alone is insufficient: one step can depend on an API exported in another file. Out-of-order execution requires satisfied dependencies or an explicit diagnostic/override action, not a generic step button ignoring the cursor.

Markdown is an authoring input, not an execution escape hatch. Convert it to a draft, fill/check criteria through bounded planning, and commit only validated executable steps. Legacy plans without checks display as unverified and need upgrade/explicit waiver before supervised continuation. Preserve their historical record without manufacturing passes.

Revision transaction:

1. Record revision_requested with target, asker, structured reason/evidence and expected plan version; persist retry/revision/pair-limit counters.
2. Increment the target revision and mark affected dependent evidence stale/needs_reverify **before** editing. Their old passes remain historical only.
3. Execute the target correction with its revision reason and current source versions. Verify the target at the resulting workspace version.
4. Reverify affected dependencies in dependency order **after** the change. Passing revalidation binds evidence to the new version; failed revalidation schedules an explicit repair attempt or blocks according to policy.
5. Only then allow downstream progression/completion. Manual and model-requested revisions use this exact transaction.

Requesting a revision is a structured tool/control operation, not regex matching of English prose. Caps and reasons survive replay. “Retry same attempt,” “reopen a passed step,” and “change the plan/check” are distinct actions. The model cannot lower a failed criterion to make it pass without a recorded authorized plan revision.

Verification publishes verify.started/result with criterion, tool-call links, observed version, exit/value and full evidence reference. Contains checks remain allowed when appropriate, but are labeled narrowly (“symbol present”), not presented as proof that an application works. A tool failure, false result and inability to run a check have different outcome codes. A guard-limited execution can retain useful work and run its authorized check; its execution stop and check result are both recorded, not overwritten by each other.

## Common execution services

Build one immutable capability bundle per run from the session's canonical workspace, base tools, approved skills and RFX. Use that bundle for planning, step execution, verification and nested reflex calls, with phase-specific subsets. Manual RFX also needs explicit session/workspace scope and the same admission/approval checks for side effects. Looking at a session never repoints a global execution registry.

The common dispatcher must check tool existence, offered/phase capability, mode, argument parse/schema, risk approval and workspace containment before execution. Malformed JSON is a rejected call/result, not `{}`. Record the original call safely even if arguments are malformed. Mutation hooks update the workspace version, source/cache validity and code index after every tool, including within a multi-tool response. Approval is bound to the actual operation/arguments; rewrites must not bypass it.

Tool started/result journaling does not magically provide exactly-once effects. If a process crashes after a shell command changes files but before the result is committed, mark that operation unknown and reconcile artifacts or ask the user. Retry automatically only when the tool's declared idempotency/retry contract permits it. Generic shell, installs, external writes and interactive browser actions are not blindly replayable.

Context packing is per run/attempt, not a shared mutable session closure. Pin the task brief, active step/revision/check, user steering and policy. Version optional source excerpts, and reread when stale. Preserve assistant tool-call/result groups together, including interrupted/multi-call responses. After demotion/compaction, recount the actual request including tool schema and image accounting, reserve the effective answer + reasoning budget, and perform a final fit check. If mandatory material is too large, stop before inference with a specific context decision. Display estimated versus model-reported token figures honestly.

Use one budget policy across all entry points: model request timeout, tool-specific timeout, active idle budget, per-attempt call/token budget, bounded retry/revision limits and optional whole-run budget. Budget exhaustion preserves cursor/artifacts as suspended/blocked. Progress is a versioned mutation, new relevant observation or completed unit—not merely that another failed tool returned. No automatic budget/sampling expansion is silent.

## Settings contract

Recommended precedence: global defaults → explicit session overrides → next-run override → explicit active-run policy update. Snapshot the effective settings on acceptance; future defaults do not silently alter accepted runs. This is a proposed change from today's partly live/global behavior and needs agreement.

Every model.call.started records policy version, requested/effective thinking mode and effort, numeric reasoning/output caps, sampling preset and concrete overrides, tool-set version and context report. Fallbacks record their cause and the next effective settings. Apply truncation fallback to the actual submitted effort, not the execution default. Automatic sampling changes require an enabled recovery policy; otherwise retain the user's chosen preset and report the failed strategy.

The UI labels “Global default,” “This session,” “Next run” and “Current run” accurately. All knobs read one shared settings store; requests show saving/applied/rejected states and preserve last confirmed values on transport failure. Persist accepted settings reliably and report a save failure. Reject unknown settings instead of silently normalizing them into a different choice.

## Durable events and UI contract

Proposed event envelope:

```text
schema_version, event_id, session_id, seq, timestamp,
run_id?, plan_id?, step_id?, revision?, attempt_id?, call_id?,
command_id?, type, payload
```

Event families include run.accepted/phase/terminal, command.requested/applied/rejected, plan.committed/superseded, step.started, attempt.finished, revision.opened, evidence.invalidated, verify.started/finished, model.started/finished, tool.started/finished/unknown, question.opened/answered/expired, incident.opened/resolved and settings.applied. One serialized writer and deterministic reducer own state transitions and enforce legal order. Event persistence failure stops new side effects and is surfaced explicitly.

Provide one session snapshot containing cursor seq, active run, plan and step state, current question, incidents, effective settings and capabilities. Existing /state, /plan and /report can remain compatibility projections of this reducer; they must not run different reconciliation logic. RFX consumes the same projection/versioned bridge contract.

The UI always subscribes to the selected session—even for CLI work. Snapshot at seq N, then stream/replay events after N; reconnect with cursor, deduplicate by identity, detect gaps and resnapshot. A disconnected observer shows “Connection lost; run state unknown” while preserving its last confirmed view. Connection loss alone is not a cancelled run. Switching session cancels old fetch/subscription publications or rejects them by selection generation. State and preferences are keyed by session + run/plan, not bare local event ID.

The installed Planner pack must be versioned/delivered alongside the host bridge, or explicitly compatibility-checked as an external extension. Render pending/running states just as authoritatively as passed/blocked; no disk-based fallback may override them. “Run remaining plan” sends continue_plan, “Next step” sends run_step with the server's cursor, and “Selected steps” is one validated scoped run—not a browser-local scheduling loop. Include this currently unversioned pack in the rollout/test scope (audit L18).

The active UI shows step/revision/attempt, current phase/tool, elapsed time, effective settings and the last meaningful observation. All accepted tool calls and verification outcomes remain inspectable after completion. Full payloads may be collapsed; progress must not require exposing private model deliberation. Use persisted timing/counters rather than fabricated progress percentages. Keep plan controls accessible after completion and through reloads.

Incidents are typed and scoped. A recoverable retry is inline activity with attempt count/next action; a successfully resolved incident stays in history but ceases to be an active error card. Waiting for an answer and a guard/budget pause have specific action panels. Exhausted retries or an unrecoverable operation yield one actionable current error with exact retry/repair target. User cancellation is neutral. Memory/indexing background failures are warnings attached to those jobs, not task failure. Do not hide genuine failures just to reduce red cards.

## Delivery and acceptance gates

Implement behind a rollout gate with versioned log compatibility. First build/test the reducer and ownership/admission; next connect the common executor, planning, controls and versioned verification; then drive every UI surface from snapshots/events. A partially migrated execution path must not write competing interpretations into the same session. No production cutover until all required slices agree.

The 22 audit assertions are the initial regression targets, not a complete proof. Expand with table-driven event replay tests at every prefix, cancellation at every model/tool/verify boundary, duplicate/reordered command/event delivery, disconnect/reload/second-tab/CLI scenarios, multi-session same-workspace races, settings updates mid-call, failed saves, exhausted retries, malformed/multi-tool output, migration of legacy/multiple plans and context fit with realistic tokenizer/tool/image payloads. Run race detection on new ownership/settings/context paths. Test crash-after-side-effect-before-result explicitly.

Finally run controlled real-model tasks in disposable workspaces: new build, follow-up after completed plan, pause/steer/resume, failed verify/revision, CLI observed in ChatUI, reconnect and restart. Capture events, screenshots and final artifacts and compare them to the same accepted trace. Do not use a claimed “fixed” live run or a passing build as substitute for these contracts.

Open review decisions: conflicting-workspace reject versus visible queue; lease retention while paused; acceptance of explicitly waived legacy steps; criteria for harmless answer-only requests; default settings scope and active-run update semantics; retry/revision limits. Recommended defaults are written above so review can be concrete, but none have been applied to the running system.
