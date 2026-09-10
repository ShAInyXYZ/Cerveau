# Backlog implementation pass — 2026-09-07

This pass addresses the twelve open areas in the private backlog. It does not
repair or resume either original voxel benchmark workspace/session. The
existing-project redesign guidance was applied using Cerveau's existing tokens
and explicit operator preferences; no replacement brand system, font or library
was installed.

## Implemented behavior

- Chat: long recovery coaching has a short disclosure summary with the exact
  original text underneath. Successful tool rows are quieter; failed calls and
  actionable incidents stay visible. Version-aware read grouping is preserved.
- Planner: active work has priority. File presence and implementation details
  move into per-step disclosures whose state survives polling. Pack provenance
  remains accessible without competing with the plan. Rounded/pill controls and
  the active-project pill restore the existing visual language.
- Floating panels: shared 24px backdrop blur with a 94% surface tint, opaque
  fallback and reduced-transparency override. Hardware/service panels use native
  popovers with keyboard, Escape and outside-click behavior. The approved chat
  transition stays a 160px opacity-only gradient; jump geometry/pulse is unchanged.
- Structured JS mutations: parse the complete proposed patch-batch state before
  writing. Reject a parseable-to-broken change, including a dangling function
  opener. Keep source/version receipts; permit incremental repair when the
  baseline was already broken, without labelling it a syntax pass. This checks
  syntax, not semantic correctness; non-JS parser coverage is not implied.
- Recovery identity: distinguish relevant stack functions/files, not only the
  error headline. Moving line numbers or revisiting an old failure does not
  grant another progress extension. Three failed repair/check cycles still stop.
- Effort: account for cumulative reasoning and output, including control-boundary
  responses, across token-window extensions. An empty truncated response gets
  one retry with thinking off; a retry is not counted as an executed repair.
- Plans: append evidence-backed, versioned implementation guidance without
  changing the original checks, fixtures, files, requirements or spent budgets.
  Explicit user delivery lists bind the step mapping/order at commit and run
  admission. Ordinary prose is not falsely labelled semantically validated.
- Memory: schema readiness gates indexing/cursors and retries with capped
  backoff. Queries use the proper query representation against unchanged passage
  document vectors. See [embedding contract and rollout](memory-embedding-conventions.md).
- Ignite: follow the configured endpoint rather than a hardcoded legacy Core;
  validate unit names, refuse known competing local profiles, throttle repeated
  requests, and keep API status polling non-waking. Remote authentication and
  forwarded-request marking remain enforced.

## Boundaries

Implementation-guidance amendments are not unrestricted replanning: changes to
acceptance, predicate meaning, scope, permissions or plan structure still need
explicit review. Delivery-order validation proves explicit ID coverage/order,
not whether a model's check adequately tests the requirement. Syntax checking
cannot prove a lighting algorithm correct. Registry checks cannot detect an
unregistered external GPU process. UI test results are not operator visual approval.

## Verification

- Release gate passed: 103 frontend tests, 13 native browser-engine tests (none
  skipped), six Python embedding contracts, full Go race suite, vet and
  production CSS assertions. Svelte check: zero errors, eight existing warnings.
- Compiled UI browser fixtures passed desktop, mobile, 320px and 200%-zoom
  layouts, retry targeting, preserved receipts, exact coaching disclosures,
  persistent Planner disclosures, 24px floating blur and the unchanged jump/fade.
  Evidence: `build/recovery-ui-A52L9Z/results.json` and adjacent screenshots.
- Actual installed idle cycle: confirmed parked state, framed notice after
  reload, editable composer, non-waking Ignite/API status reads, configured
  Core wake via Ignite, then model readiness and notice removal. Legacy remained
  stopped and original benchmark journals/sources were unchanged. Evidence:
  `build/backlog-live-idle-NJUKF2/result.json`. This verifies local Ignite entry,
  not the physical phone/tailnet transport. An earlier attempt timed out after
  20 seconds while production's calm-state polling interval is 30 seconds;
  retained failure: `build/backlog-live-idle-CPtkAo/result.json`.
- Real Qwen3.8-27B recovery, Medium/Default, production context manager (262144):
  seeded line-160 syntax defect fixed, original foundation rechecked, continuation
  marker written and verified. **24.294 seconds, six model requests**, one edit,
  all three steps passed. Evidence: `build/recovery-live-SUkU5d/baseline/evidence.json`,
  `requests.json`, `final-snapshot.json` and `protection.json`. This is a fresh
  disposable regional fixture with the actual model, not the full voxel game,
  and not a resume of either original session.
- Live embedding route returned a normalized 2048-dimensional query vector
  using `nemotron3-query-v1` against unchanged `nemotron3-passage-v1` documents.
  The actual hybrid lookup then exposed Typesense's 4000-character URL cap;
  the correction uses POST `/multi_search` instead of encoding the vector in
  a GET URL. The corrected read-only hybrid lookup passed: five hits in 36 ms.
  Dense 2048-vector transport, malformed-wrapper and lexical-fallback regressions
  passed with the memory race suite. No stored vectors or schema were changed.

Commands: the release is built by `node scripts/build-release.mjs`; compiled UI
checks use `node scripts/qa-recovery-ui.mjs`, both with the existing Chromium and
Playwright paths. The bounded real-model fixture uses
`node scripts/qa-recovery-live.mjs baseline --run` with privately inherited local
Core credentials. No credential is written into these documents or evidence.

## Deployment

Initial fully gated build `a3a8367-dirty-c75970fff1f3` was installed from
`build/cerveau-0.6.0-alpha-LABRIG-p72Cnz`, with verified recoverable binaries in
`/home/shiny/.crv/install-backup-harness-recovery-0xv5bM`. Core, embedder and
Typesense were healthy; corrected Ignite was restored. The following release
incorporates the live-discovered hybrid POST correction.

Final revision **`a3a8367-dirty-d8ac016fa1ad`** passed the full release gate and
was installed from `build/cerveau-0.6.0-alpha-LABRIG-QhWDFt`. Recoverable old
binaries and the verified installation receipt are in
`/home/shiny/.crv/install-backup-harness-recovery-LFlQv2`. There were no active
Cerveau runs. Only the harness and Ignite restarted; Core PID 3712156 and
embedder PID 3704597 stayed unchanged. Installer checks verified the protected
benchmark sources/journals, settings and installed bytes.

Final read-only health, service and served HTML/JS/CSS checks passed:
`build/backlog-final-verification.json` (2026-09-07T06:03:52Z). Harness, Ignite,
selected Core, embedder and watchdog are active; model, embedder and Typesense
report healthy; legacy llama remains inactive. Repeated live hybrid memory
lookup returned five hits in 35 ms, with no writes; retrieval relevance was not
scored. Two verification invocation mistakes (omitted receipt argument and
assuming Ignite's plain-text status was JSON) were corrected and rerun; neither
was treated as a successful check.

This deployment receipt was appended after the source fingerprint was built;
no runtime source changed after that build. The full voxel benchmark remains
unverified; visual acceptance still belongs to the operator.

Subsequent recovery-button regression and installed compatibility correction:
[legacy saved-plan recovery](legacy-plan-recovery-2026-09-07.md), revision
`a3a8367-dirty-4daba8a034bf`. This supersedes the blanket rejection of genuinely
pre-binding, previously executed saved plans; their delivery order remains
explicitly unvalidated, rather than inventing a new mapping or replacing progress.
