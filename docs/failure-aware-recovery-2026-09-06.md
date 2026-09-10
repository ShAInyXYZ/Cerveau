# Failure-aware recovery

Implemented in the source tree on 2026-09-06, then installed at the user's request as `a3a8367-dirty-619383328f9d`; the local installation receipt is retained privately. No production session was resumed and the Brain Core was not changed by the installation.

The next live run exposed a remaining recovery stall. See the [follow-up diagnosis, bounded output retries and plan UI changes](recovery-stall-and-plan-ui-2026-09-06.md). The execution contract below describes that installed baseline; the follow-up supersedes its output-retry accounting and report labels.

## Why

The inspected Minecraft run `85a5caa06c74e04b541d5d09` stopped on meshing. Journal event `evt_000982` invoked `head -n 657 world.js > world.tmp && mv world.tmp world.js`; `evt_000984` confirmed the truncated file. Later verification reported `SyntaxError: Unexpected end of input` at line 754. The recovery attempt spent its allowance inspecting without repairing. Earlier checks remained historical passes despite sharing the broken source.

The previous patch-matching fix was operating correctly: it rejected a mismatched patch without applying its hunks. Shell writes bypassed that structured-edit protection. More iterations alone would not address that failure mode.

## Execution contract

1. Before an attempt, preserve declared regular plan files as content-addressed, SHA-256-verified copies alongside the session journal. Append a manifest, including unavailable entries. This is not a workspace rollback or Git repository.
2. A failed-verdict or interrupted/revision-reason attempt gets a dedicated recovery brief: the recorded verdict, recent tool results, recent shell/structured-edit calls, and available snapshot identities. Tool snippets are explicitly bounded and are evidence, not instructions or a claimed diagnosis.
3. A recovery-only `recovery_read` capability reads preserved UTF-8 source by SHA-256 or full recorded tool evidence by event ID, in bounded pages. Both are restricted to the current plan. Copies are verified again when read. The capability does not restore files and disappears before the next ordinary step.
4. Recovery instructions require targeted diagnosis, syntax checks first for syntax failures, comparison against any preserved source, structured repairs, and the unchanged committed check. Syntax-first and repair selection are model instructions, not a universal language-aware repair engine.
5. Built-in `bash` during recovery, including the committed recovery verification, runs through Bubblewrap with a read-only host filesystem, private `/tmp`, and no network. Shell truncation, rename and interpreter writes obey the mount boundary. Missing Bubblewrap or failed isolation means an error, never an unprotected fallback. Structured `read`, `edit`, `apply_patch` and `write` remain available under their normal registry policy.
6. Before a step can mutate declared shared files, earlier matching passes become `needs_reverify`, durably. Empty file lists mean unknown coverage and conservatively invalidate earlier evidence. After the active check passes, affected earlier checks run again before advancing. A failing prerequisite blocks continuation. A resumed `needs_reverify` step runs its check, not a model rebuild.
7. Existing attempt, iteration, repeat-error, time and token guards remain unchanged. Failure cannot be relabelled as success by model prose. There is no unlimited recovery loop.

## Commands and UI

The chat incident action is now **Recover step and continue plan**. It sends the existing plan-bound `step` command with `continue_plan: true`. The backend owns repair, verification and subsequent plan execution in that one run.

An explicit single-step command still stops after that step. Selected execution never runs a prerequisite check outside its selection: it leaves the evidence stale and hands back for a wider authorized run. `continue_plan` is rejected on non-step commands.

The existing run-status line displays a persisted `recovery_phase`: diagnosing, repairing, rechecking, or continuing. These are emitted by backend operations, not timers. They describe the current activity, not a guarantee that a repair will succeed. Ordinary tool activity, pause/stop controls and canonical plan status remain intact. The long-composer layout issue stays deferred.

## Boundaries

- Copies cover at most 64 declared regular files and 8 MiB per attempt, at most 1 MiB per file. Directories, symlinks, missing, oversized and non-local paths are explicitly unavailable. No recursive or full-workspace backup is claimed. Copies deduplicate by content and are not automatically deleted.
- These are pre-attempt source bytes, not a complete filesystem/permission snapshot or a backup of every intermediate edit. Newer work must be compared and preserved. Old sessions have no retroactive pre-failure copies.
- Dependency freshness relies on declared file metadata; it is not whole-program dependency analysis and does not discover undeclared effects. Conservative invalidation can require checks even when the model ultimately makes no edit.
- The shell restriction applies to built-in Bash and callers delegating to it, not arbitrary native extensions or the entire harness. It is not a complete adversarial security sandbox. Normal first-attempt shell behavior is unchanged.
- Recovery checks that require network or writing into the project will fail under this shell policy. The harness does not silently relax it; such tasks may need an explicit future writable-artifact policy. Private `/tmp` is available for scratch work.
- No guarantee is made that a model will repair every failure, or that a narrow declared check proves untested behavior.

## Verification

Tests were introduced before implementation and initially failed on the missing recovery functions. The focused suite exercises verified copies, corruption refusal, no implicit restoration, failure-tail preservation, current-plan scope after reopening, read-only shell truncation refusal, isolation-unavailable refusal, repair → prerequisite recheck → continuation, failed prerequisite handback, stale-evidence recheck without model execution, and explicit continuation versus existing single-step behavior.

Commands:

```sh
go test -race -timeout 90s ./...
go vet ./...
cd panel
./node_modules/.bin/vitest run
./node_modules/.bin/svelte-check --tsconfig ./tsconfig.json
npm run build
```

Go race tests and vet passed; panel Vitest passed 72 tests; Svelte check reported zero errors and 16 existing warnings. Panel production build was also checked. No real-Core recovery success is claimed.

Browser fixture: `scripts/qa-recovery-ui.mjs`, using the existing Playwright/Chromium runtime. Desktop 1440×1000 and mobile 390×844 checked retry command scope, all four server-projected recovery labels, horizontal overflow and page errors. Evidence: `build/recovery-ui-UzWxRk/results.json` and sibling screenshots. All API requests were mocked; this did not touch localhost:7700, production sessions or the Core. The initial fixture lacked a user message and therefore correctly had no retry button; adding a representative message fixed the fixture, not product code.

DGV ownership: `dgv/run-stepwise` and `dgv/loop-contract`, node `recovery`.
