# Browser recovery tools — 2026-09-06

Scope: improve Cerveau's harness/tools after the Minecraft step-5 stop. The original benchmark workspace and journal remain read-only. Automatic plan amendment and the broader visual redesign are pending in `docs-private/tobefixed.md`; its status index separates shipped behavior from remaining design work.

## Observed run

Session `20260905-160857-minecraft`, run `ac0a51719d6b502d7df1940a`, 09:20:43–09:38:47 UTC, 37 model calls. A model round is not necessarily a repeated operation.

- Steps 1–4 reached verified state before step 5. Meshing was rechecked after the seam repair; the later recorded fixture result was 528 naive faces, 25 quads, 100 vertices.
- The app was created and served. Browser evidence `evt_003674` reached runtime state but exposed `world.placePlayer is not a function`. The model repaired that and a subsequent WebGL program-order error.
- Probes at events 3710, 3718 and 3726 each took about 25 seconds and produced neither DOM nor eval, including the trivial `1+1` probe. Three failed repair/check cycles paused recovery. `evt_003742` is the final unchanged committed check, not a successful browser result.
- `check_page` ignored `cmd.Run()` errors and the 25-second cancellation. With no captured console errors it claimed the page loaded cleanly even when no DOM existed. This was a demonstrated harness defect, not proof of a clean app.
- Source inspection also found costly synchronous chunk/lighting initialization and an initial mesh-upload path that only visits already-uploaded mesh keys. These are findings for the app's next recovery; they were not repaired out-of-band. Main-thread starvation was a hypothesis, not an established sole cause of the production timeout.
- The committed predicate checks `window.__world` and HUD text only. It can be true without visible terrain or successful interaction. Its original expression is preserved; passing it must not be represented as completion of browser acceptance item 9.

## Implemented changes

- `check_page` separates process failure, cancellation, timeout, missing DOM, missing eval and an unsettled evaluation Promise from a completed check. Partial true output cannot make an incomplete browser run pass. Console silence is described only as an observation.
- The CLI browser resolver prefers an already-installed dedicated Chromium headless shell, retaining `CRV_CHROME` as an explicit override. Each report identifies the executable. There is no silent retry with another engine after a failed invocation.
- Browser runs own an isolated temporary profile and process group. Cleanup targets only that group/profile. Both streams retain bounded head/tail evidence (128 KiB each); a descendant holding an output pipe cannot keep a cancelled call alive indefinitely.
- Eval instrumentation reports harness installation, DOM content loaded, window loaded, eval started and eval finished. These are loading observations, not screenshot or responsiveness proof.
- Verification verdicts preserve diagnostics/output alongside errors and store a browser failure category. Recovery receives guidance to inspect server delivery and loading stages, retain the committed expression, and test a specific hypothesis. Budget and failed-repair limits are unchanged.
- Evaluation verification awaits the declared expression before converting its result to a boolean. Previously a Promise resolving to false could pass because the Promise object itself is truthy. Real-browser tests cover synchronous true/false, resolved true/false, rejection and an unsettled Promise; the declared expression is not rewritten in the plan.
- Native `serve action=probe` checks an owned port/path with bounded GET/HEAD, status, MIME, response/file hashes and server identity. It does not accept arbitrary URLs or follow redirects, use a proxy, or relax recovery-shell network isolation.
- Static serving reports its root and refuses same-port reuse for a different directory. `os.Root` confines file opens, including symlinks introduced after startup. Changed/replaced root identity fails closed.

No additional package, global tool, persistent browser manager or generic network capability was installed.

## Verification record

Before the repair, all four new fake-browser diagnostics tests failed: process exit ignored, timeout with partial true treated as success, empty DOM treated as clean, and missing eval blamed on an unobserved throw. The timeout regression also exposed a 30-second descendant-pipe wait despite a 150 ms context.

After implementation, those four tests passed in 0.156 s total. Focused loop tests prove incomplete browser outcomes cannot pass, retain output, preserve the committed check, and remain distinct from an actual false eval.

Commands used during development:

```sh
go test ./internal/tools -run '^TestCheckPage(ReportsProcessFailureNotCleanLoad|TimeoutRetainsPartialEvidenceWithoutPass|NoDOMFailsClosed|MissingEvalDoesNotBlameThrownExpression)$' -count=1
go test ./internal/loop -run 'TestBrowser|TestCompletedBrowser|TestVerifyEval' -count=1
go test -race ./internal/tools -run '^(TestServe|TestRegistryAutoFixesOccupiedPort)' -count=1
go test ./internal/tools -run '^TestCheckPageRealBrowser' -count=1 -v
```

Initial real-Chromium result: the simple WebGL fixture reported true and all five loading stages but timed out after 25.029 s without DOM. The intentionally blocked fixture was correctly reported as a timeout after 4.013 s. This separately reproduced a browser-completion problem on a trivial page; attributing every production timeout to the generated app would be wrong.

### Browser-selection experiment and final fixture results

Disposable 130-byte local fixture, same SwiftShader/virtual-time/dump-dom flags and owned lifecycle helper:

- Full Chromium cache revision 1234: no DOM within the 5-second diagnostic bound, despite a JS console marker. Disabling extensions, background services, or both did not resolve it. Automation/default-browser flags also did not resolve it. An older full Chromium was not a successful alternative.
- Already-installed dedicated headless shell revision 1234: exit 0, DOM and JS marker, 80.98 ms. Executable `/home/shiny/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell`, version `Google Chrome for Testing 151.0.7922.34`.
- Existing full tools suite with full Chromium: FAIL, ten ordinary browser checks timed out, 277.092 s total. Same suite with a command-local dedicated-shell override: PASS, 7.160 s. No production environment/config was changed by that experiment.
- After changing the resolver default, real WebGL fixture: PASS, 306 ms, true evaluation, DOM present, all five loading stages.
- Intentionally blocked page: correct timeout, 4.009 s, no eval; instrumented copy removed. A never-settling Promise: correct `eval_timeout`, not a false predicate or completed check.
- HTTP-served ES-module fixture: PASS. The four final real-browser cases completed in 4.561 s total. Temporary diagnostic experiments were removed; permanent regression fixtures remain in the test suite.
- Combined tools and loop race suites: PASS (12.835 s and 12.377 s respectively) before the additional awaited-verification regression; the full release gate reruns all tests on final source.

The full-browser CLI hang's internal Chromium cause is not proven. The tested dedicated-shell selection resolves the reproduced tool-level completion failure without changing the app, installing a dependency, accepting partial results or increasing timeouts.

### Release and installation evidence

`node scripts/build-release.mjs` runs packaging gates, frontend tests/check/build, the full Go race suite, vet and whitespace checks before producing a new checksum-verified bundle. `build/<bundle>/build.json` identifies the exact source snapshot. Installation is allowed only after a fresh check confirms no active Cerveau runs and an already-awake Core; the harness alone is restarted.

The installation receipt is written outside the source snapshot to `/home/shiny/.crv/install-backup-harness-recovery-*/install.json`. It records old/new binary hashes, live build identity, unchanged protected files (including both original benchmark sources and journal), unchanged sampling/thinking, and the retained Core process owner. This document does not claim that the production benchmark was resumed or completed.
