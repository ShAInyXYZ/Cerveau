# Recovery must distinguish code defects from disputed checks

## Latest production evidence

Read-only diagnosis of Minecraft run `1b44ee01c31c7219fd739b3c`, 2026-09-06 07:50:19–07:54:33 UTC. It used eight model requests, ten requested tool calls, three harness verifications and one structured edit. The eight-round stop was intentional: the edit did not change the failing result, so it earned no checked-progress extension. This was not a service crash.

- Original plan `evt_000030` requires `naiveFaces >= 300` for its chosen flat fixture. The user's voxel benchmark does not specify this threshold.
- `evt_002948` observed `{naiveFaces:256, quads:1, vertices:4}`. The model correctly identified the flat-surface/count mismatch in `evt_002941` and `evt_002953`, but treated the generated threshold as a reason to change the fixture.
- Edit `evt_002979`/result `evt_002982` added comments and negative-Y branches after the existing out-of-range stone return. Those branches cannot create the additional visible bottom faces claimed by the comments.
- Checkpoint `evt_003017` and final check `evt_003027` still fail with `Error: too few`. The original plan and current source remain untouched by this diagnosis.
- Separate source-derived concern: the current emitter appends six unindexed vertices per quad but increments reported vertices by four. The saved count check is not proof of truthful geometry statistics or surface coverage. This concern remains a benchmark implementation/criterion review item, not a reason to silently change code or checks in a harness release.

## Harness design

1. Preserve previously passing shared-file evidence when status changes to `needs_reverify`. Label it historical, never current proof. Its known constraints must not disappear from the next repair's context.
2. Offer a read-only `read_plan_step` tool for the exact saved step definition and verification, bound to an immutable current-plan snapshot. Do not require source-history searches to recover plan commands.
3. Offer `request_verification_review` for an evidence-backed contradiction, with reason, observed evidence IDs and an optional proposed criterion. The harness captures plan/step/original-check identity; model-supplied approval is not authority.
4. Persist the proposal and retain the original verification result. A review request blocks automatic continuation without granting another repair attempt. It never installs or runs the proposed criterion, and cannot prove the step passed.
5. Clearly distinguish immutable execution criteria from unquestionable requirements: preserve the user's original contract, never distort fixtures or fabricate statistics to satisfy an unsupported generated threshold. Request review instead of guessing a new acceptance condition.

`read_plan_step({index: 0})` uses zero-based indices and returns the complete saved step, check, state and evidence snapshot. The normal context summary remains bounded to 6,000 bytes. Each model-visible tool result includes its actual journal receipt ID; receipt headers are not source text.

`request_verification_review` takes `reason`, one to eight `evidence_event_ids`, and an optional `proposed_verify`. Evidence must be a paired tool observation or matching harness verification from this session's current plan. Historical observations are explicitly marked. The original check is copied from the committed plan, not supplied by the model. Unknown approval fields, mismatched identities and ambiguous tool-result pairing are rejected.

The harness stops the remaining tool-call batch, reruns the original check, and records the proposal plus actual result. Even a passing original check does not bypass a requested review. A generic retry receives the saved concern and still uses the original check; it cannot install a proposal. No amendment endpoint or automatic criterion approval is introduced by this change.

The persisted handoff note also carries the fresh verdict. Replay validates plan/step/session/run identity, original-check hash, proposal identity and the latest exact check/result pair before restoring blocked state. An interruption before the subsequent PlanState write therefore does not lose the review. Later authoritative state or a new plan supersedes it; duplicate notes do not add attempts. Stale plan snapshots are rejected before execution or saving state, not treated as ordinary repair errors that an old passing check could override.

This task does not amend the original Minecraft plan, edit its source, retune the Brain Core or begin the deferred visual redesign. Applying a criterion amendment requires a separate, explicit operator decision; generic recovery is not approval.

## Verification and delivery

Focused regression checks passed:

```sh
go test ./internal/loop -run 'TestVerificationReviewStopsWithoutChangingCheckOrRunningProposal|TestStepRunExposesExactSavedPlanContract' -count=1
go test ./internal/loop -run 'TestVerificationReview|TestStepRunExposes|TestSupervisorVerification' -count=1
go test -race ./internal/loop
```

The first command failed on both assertions before integration and passed afterward. Tests exercise a blocked fixture with an unexecuted proposed command, a subsequent write in the same model batch, ordinary retry under the unchanged check, durable proposal state, exact plan reads, receipt visibility and a disputed check that actually passes. These deterministic tests use temporary workspaces and a scripted model, not the production Minecraft session.

Six replay regressions passed, including failed and passing actual verdicts interrupted before PlanState persistence, malformed/foreign/stale identity rejection, legacy envelope compatibility, command and contains receipts, duplicate notes and later state supersession. A separate regression proves a stale plan cannot execute or save state even when its old check would pass.

Final `node scripts/build-release.mjs` — **PASS**: five packaging/safety tests, 101 frontend tests, full `go test -race -timeout 90s ./...` including all 227 loop tests, `go vet ./...`, `git diff --check`, production assets and binary checksums. Svelte checking reports zero errors and eight existing warnings; the existing bundle-size warning remains. No frontend source was changed in this task, and no new visual approval is claimed.

Live model adoption of the review tool and voxel completion remain **unverified**. No production retry was submitted. The generated meshing criterion and truthful vertex/coverage evidence still require review; the harness does not infer approval from a generic Resume action.

## Installation receipt

- Installed at 2026-09-06 10:29 CEST, after an immediate `/api/sessions` check confirmed `running: []` and `/api/idle` confirmed the Core was active.
- Release `0.6.0-alpha LABRIG`, revision `a3a8367-dirty-c71e1f303281`, bundle `build/cerveau-0.6.0-alpha-LABRIG-v7w9uo`.
- Recomputed source fingerprint matched `build.json` before installation: `c71e1f303281ae3bb5932690d0407a8949bf3447acbd8351fcae4dab52107e3a`.
- Verified backup/receipt: `/home/shiny/.crv/install-backup-harness-recovery-nIT7G6/install.json`. Only `crv` and `crvcli` were replaced; previous binaries remain recoverable in the backup. Only `cerveau.service` was stopped and started.
- Initial bundle `SlLDKi` and preparation `d3r9Dn` were superseded before any service stop because a final replay normalization/test changed the source fingerprint. Both have `DO-NOT-INSTALL.md` markers and were never installed. Full gates were rerun for `v7w9uo`.
- Live `/api/build` confirms the installed revision. Harness PID is `2362255`; selected Core PID `2139642` and embedder PID `476517` stayed unchanged. All three services are active/running.
- `/api/health` reports model, embedder and Typesense healthy. Startup tool-call canary passed. `/api/rfx` reports 21 reflexes and no errors; Planner 1.5.1 remains builtin, with the installed 1.5.0 copy ignored.
- Installer verification confirms original Minecraft `world.js`, its session journal, configuration, service definitions, installed packs/helpers and sampling/thinking settings unchanged. No session was running after installation.
- Served HTML, JavaScript `index-PxFDABQ9.js` and CSS `index-DkPfXu7f.css` match packaged assets byte-for-byte.

This receipt and final DGV delivery notes were added after the build and do not represent another binary change. The deferred UI redesign remains in the private backlog.
