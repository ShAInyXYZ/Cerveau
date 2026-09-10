# Legacy saved-plan recovery compatibility

## Observed regression

After installing `a3a8367-dirty-d8ac016fa1ad`, the existing MineKraft recovery
button reached the server but failed admission with `plan predates explicit
delivery-order binding`. This was a harness compatibility defect, not a fresh
voxel test failure. The earlier controlled recovery fixture did not include an
old plan whose original user request had an explicit delivery list.

Read-only journal inspection found the original plan at `evt_000030`, with no
delivery metadata or step IDs, and historical paired harness verification at
`evt_000241` / `evt_000245`. Failed recovery clicks ended at `evt_001104`,
`evt_001107` and `evt_001110`. Admission was also too late: those clicks had
already saved the blocked step as pending and reset its attempt counter, while
retaining the failed verdict. No benchmark file was repaired or executed by
this investigation; the journal was not rewritten to undo those clicks.

## Compatibility boundary

Resume must preserve the saved plan identity, checks, order and historical
evidence without inventing positional milestone mappings. An old unbound plan
is not retroactively certified as following the user's delivery order. In this
case its eight saved steps do not match the six requested delivery milestones;
that difference must remain explicit, not be hidden by a fabricated mapping.

Compatibility is limited to genuinely legacy-shaped plans with their own
paired, exact-check historical harness evidence. Modern null/partial contracts,
unexecuted lookalikes, foreign receipts and changed checks must not gain that
exception. Bound plans retain strict delivery validation. Validation must run
before recovery-state mutation and again at execution admission. A successful
legacy resume surfaces the validation boundary while retaining all original
requirements and final acceptance checks.

## Verification and installation

- Real Recover command-path regression passed under race detection:
  `go test -race -timeout=25s ./internal/api -run TestHTTPRecoverLegacyDelivery`.
  This sends the actual `kind:step`, `continue_plan:true` API command with a
  saved legacy plan and pending failed verdict. Mocked model/tools repair the
  correct step, recheck the prerequisite and continue. Plan identity, exact
  checks and historical events are retained. All file effects are temporary.
- Focused legacy admission and bound-order race tests passed (1.043 seconds).
  Modern null/empty markers, unpaired/changed/prior-plan checks, foreign copied
  receipts, missing session envelopes and wrong caller identity are rejected.
- The read-only actual-journal probe passed after session-provenance hardening
  (0.066 seconds), accepting `evt_000030`, retaining all eight steps and
  recovery target index 1. It compares journal bytes and effective plan/state
  before/after, and never opens a writer or executes a model/check. Command:

  ```sh
  CRV_LEGACY_DELIVERY_SESSION=20260907-014354-minekraft \
  CRV_LEGACY_DELIVERY_JOURNAL=/home/shiny/.crv/sessions/20260907-014354-minekraft/events.jsonl \
  GOCACHE=/tmp/cerveau-native-check-gocache \
  go test ./internal/loop -run '^TestLegacyDeliveryActualJournalAdmissionReadOnly$' -count=1 -v
  ```

- The paired API regressions passed under race detection (1.250 seconds):
  `go test -race -timeout=30s ./internal/api -run 'TestHTTPRecoverLegacyDelivery|TestHTTPRecoverInvalidDelivery'`.
  Rejected modern/null metadata produces no new PlanState, model call or tool
  call, and preserves blocked status, attempts, verdict and prior history.
- Full release gate passed: 103 frontend tests, 13 native browser tests,
  six Python contracts, Go race suite/vet and production CSS checks. Svelte
  reported zero errors and the same eight existing warnings. No UI assets
  changed in this compatibility fix.

Installed revision **`a3a8367-dirty-4daba8a034bf`** from
`build/cerveau-0.6.0-alpha-LABRIG-82Y64y`. Verified recoverable receipt:
`/home/shiny/.crv/install-backup-harness-recovery-F6bGiw/install.json`.
No active Cerveau runs existed. Only harness/Ignite restarted; Core PID 3712156
and embedder PID 3704597 stayed unchanged. Protected benchmark files, journals,
settings and installed bytes passed verification. Health/services/exact served
assets passed at 2026-09-07T06:33:17Z:
`build/legacy-recovery-final-verification.json`.

These test/deployment results were appended after the release fingerprint;
no runtime source changed after the build.

Older command/eval journals without linked tool-evidence receipts remain
conservatively outside this compatibility exception; they need explicit plan
review. The observed MineKraft journal contains those receipts. No live
benchmark resume is part of these checks.
