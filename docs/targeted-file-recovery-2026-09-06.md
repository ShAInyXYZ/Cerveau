# Targeted file tools and durable recovery — 2026-09-06

Follow-up to [the read-stall diagnosis](recovery-read-stall-2026-09-06.md).
The operator authorized implementation so retry can use acquired evidence,
repair the failed step, and resume the existing plan. Original Minecraft files,
its production session and the W8A16 Core are outside the mutation scope.

## Implemented contract

- `read` retains inclusive `from_line` / `to_line` and byte `offset`. Its first
  `[read {...}]` receipt identifies actual returned lines and raw byte span,
  source SHA-256, partial-line boundaries, EOF and exact continuation. Complete
  lines are preferred; oversized lines are explicitly paginated as UTF-8-safe
  fragments. Requested coverage is never presented as returned coverage.
- `expected_sha256` optionally binds a read continuation or edit/write to the
  inspected source. A stale version is refused. Unqualified read continuation
  resets when the source changes; explicit ranges do not change that cursor.
- The complete presented read page, including metadata and continuation text,
  fits the chat replay ingress budget. Replay must not cut source beneath an
  unchanged coverage receipt. This boundary has its own regression coverage.
- `edit` still replaces unique old/new text, not bare moving line numbers.
  Successful mutations return bounded changed-byte excerpts, changed lines and
  before/after hashes. Patch hunks validate sequentially and recheck their
  prevalidated versions at dispatch. Omitted/null write content is refused.
- `recovery_read` searches decoded source fields newest first, supports a path
  filter, and returns stable source refs with exact byte offsets. It excludes
  duplicate raw arguments and shell-command noise. Raw event access remains.
  Tool outcomes are paired by run/tool/call identity; an interrupted orphan
  cannot borrow a later call's success. Historical read coverage is derived from
  captured source, not an old misleading requested-range header.
- Step execution journals a bounded index of acquired observations. Retry and
  host restart restore only the current plan/step/verification-contract record,
  linked to the original paired call/result and output hash. The compact prompt
  prioritizes source evidence, rechecks file hashes, and labels stale reads and
  historical source explicitly. It does not preserve model guesses as facts.
- A recovery checkpoint asks for a supported targeted repair or a precise
  blocker. Repeating only retained evidence for four rounds without a workspace
  change stops with an explicit no-progress reason. Ordinary iteration/time/
  token/error guards remain. A guard handback records retained observations,
  repair calls and the last observed tool failure rather than losing the work.
- Follow-up after the first actual-copy run: one existing iteration-extension
  slot may preserve the same recovery window after at least 1,024 genuinely new
  current-source bytes were inspected. Coverage is validated against actual
  file bytes and unioned so repeats/aliases/overlaps do not buy more calls.
  Historical coverage is seeded on replay; stale versions do not count. This
  diagnostic continuation is available once per attempt. Further extensions
  require workspace changes; the existing overall extension limit and all
  other guards remain. It is inspection continuity, not proof of repair.
- The unchanged committed check and earlier shared-file rechecks remain the
  authority for continuing the plan. A successful edit is not a passing check.

## Regression evidence

Tests were added before implementation and reproduced the missing contracts.
Coverage includes large ranges, exact Unicode/oversized-line continuation,
stale versions, malformed writes, sequential patch preconditions, bounded
mutation receipts, newest-first source pagination, decoded source provenance,
reused call IDs across interruption, and actual new-read receipt decoding.

Recovery-memory tests cover replay, stale-source detection, plan/step/check
isolation, bounded deduplication, modified/orphaned evidence and navigation
references. A real-loop scripted regression exhausts its first read attempt,
receives acquired regions on retry, edits the target, passes the unchanged
check and continues to the next step. This is not real-model proof.

Release checks passed: full Go race suite, Go vet, 75 Vitest tests, and Svelte
check with zero errors and the existing 16 warnings. Production panel build,
release identity/safety checks and four RFX packaging tests also passed.

## Live evidence and delivery

The opt-in runner `scripts/qa-recovery-live.mjs` uses a separate test-only host,
session store and workspace with the existing Core. It has no model lifecycle,
idle-parking or indexing service. Bash is read-only throughout; structured
tools are confined to the disposable workspace. Prepared copies are verified
and retained. Production workspace/journal hashes and Core PID/state are
compared before and after.

Preparation-only checks passed with zero model requests:

- `build/recovery-live-RuZo79/baseline`
- `build/recovery-live-F8qRMH/minecraft-copy`

Live results so far (no new build installed yet):

- Regional baseline: `build/recovery-live-JPrcBN/baseline`, PASS, 17.899 seconds,
  seven real-model requests. It read lines 150–175 and used `expected_sha256`
  to replace only `return value * ;` with `return value * 2;`. The failed step,
  earlier prerequisite recheck and following step all passed. Original files,
  journal and Core PID/state were unchanged. This first baseline used the real
  loop without a window manager; the runner now wires production packing for
  the larger copied-session case.
- Regional baseline repeated with production packing:
  `build/recovery-live-TKz9YU/baseline`, PASS, 17.88 seconds, seven real-model
  requests. The guarded regional edit, failed-step check, prerequisite recheck
  and following step passed. Protected source/session and Core PID 1913120
  remained unchanged. This run includes the source-continuity implementation.
  The later whole-read-page ingress correction and test-fixture safeguards
  have regression evidence, not another real-model run.
- First actual-copy run: `build/recovery-live-oxHvQh/minecraft-copy`, FAIL,
  196.11 seconds, 16 model calls, zero edits. Accurate reads were observed, but
  the eight-call windows still ended during source acquisition. The second
  attempt received retained evidence but performed more inspection. This
  prompted the bounded, verified-source continuity change above; it is not a
  successful recovery result. Original source and checks were left unchanged.

The actual-copy repeat `build/recovery-live-a3ivqf/minecraft-copy` ended after
601.291 seconds and 22 request attempts with no edits: recovery is UNVERIFIED.
It exposed two independent validation-fixture problems:

1. The inherited proxy client timed out after 110 seconds, while the production
   model client allows ten minutes. The model had found and retrieved an older
   public API tail, but no repair had landed before that timeout at
   03:58:07.527 UTC. The recovery fixture now uses the production transport
   timeout; its outer 600-second/call/token caps are unchanged.
2. Production Cerveau considered itself idle while the separate test process
   was using the Core. At 03:58:12.582 UTC it requested its configured one-hour
   idle park; the watchdog stopped the Core beginning at 03:58:14.810 UTC.
   Later test requests activated its socket, while repeated park requests
   caused further stop/wake cycles. This is not an operator action or a model
   repair. No Core lifecycle command or configuration change was issued by
   this work, but the unchanged-Core assertion correctly did not pass.

The test's first protection finalizer stopped before writing `protection.json`
because the Core was not active/running. Original workspace and journal
identity were checked separately after the run; see
`build/recovery-live-a3ivqf/minecraft-copy/post-run-audit.md`. The runner now
fails closed unless parking is disabled or its actual countdown exceeds
720 seconds. An unrelated hold is not enough. It never disables/snoozes idle
or wakes a parked Core; startup still requires active/running. End-of-test
protection evidence now records changed/inactive Core state instead of losing
the record to an early assertion. These safeguards have regression coverage.
Further real-model tests are paused.

Do not interpret preparation, scripted tests or the smaller baseline as a
full voxel pass. The production command path holds its own idle timer; the
interference above concerns the isolated test process, which production did
not know was active.

## Delivery state

The release is built but not installed. Port 7700 still serves
`a3a8367-dirty-9c527991491f`. The earlier staged backup
`/home/shiny/.crv/install-backup-harness-recovery-gh8Vue` is retained but must
not be used for installation: its expected Core process state predates the
idle-parking interference and its bundle predates the final ingress fix.

Final build: `build/cerveau-0.6.0-alpha-LABRIG-qUCJw0`, revision
`a3a8367-dirty-e29f1d6979f7`. Full Go race suite and vet, 75 Vitest tests,
Svelte check (zero errors, 16 existing warnings), production panel build,
release identity/safety checks, and packaged binary/manifest checksums passed.
This bundle includes the replay ingress fix and test safety corrections.

No production harness restart, installed-binary replacement or original
benchmark edit was performed in this task. Installing the host normally runs
its startup `ToolCanary`, which can wake a parked Core by socket activation.
Confirmation was requested before proceeding with that side effect. A future
installation needs a fresh verified backup, an idle production session check,
and verification of the served build and protected data afterward.

## Separate generated-plan problem

The original mesh step demands `naiveFaces >= 300` and `vertices === 4 * quads`.
Neither numeric requirement appears in the benchmark prompt. In the current
chosen flat fixture, grass occupies y=0 for all X/Z, air is above it and stone
is below the world. Deterministic neighbors hide side and bottom faces: a
16×16 chunk therefore exposes 256 top faces, not 300. This is a source-derived
finding; the original truncated file could not execute that check yet.

The current emitter also uses six unindexed vertices per quad. Switching to
four indexed vertices could be legitimate; reporting four while emitting six
would fabricate statistics. Passing these count checks alone would not prove
surface correctness. Do not silently change the original plan's checks or
patch benchmark code as part of a harness release.

## Deliberate limits

SHA preconditions are optional for compatibility; they are not filesystem
transactions or atomic compare-and-swap against unrelated external writers.
Writes preserve existing in-place/hard-link behavior. Recovery source pages
cap decoded bytes; JSON encoding can increase their serialized size. Historical
source and verified byte copies are never restored automatically and may be
broken or incomplete. Better evidence/tools do not guarantee the model will
solve every implementation defect or finish the full voxel benchmark.
