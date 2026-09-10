# Recovery evidence and memory — 2026-09-07

This change improves Cerveau's recovery context and check tools. It does not repair, rerun or pass the MineKraft benchmark. The inspected workspace and session remain evidence, not implementation targets.

## Observed failure

Scope: session `20260907-014354-minekraft`, run `8ab22633c9f093e4fd9ab3eb`, events `evt_000001`–`evt_000733` in `/home/shiny/.crv/sessions/20260907-014354-minekraft/events.jsonl`. The run lasted 38m53s and made 77 model calls and 90 journaled tool calls. Counts below describe that run only.

| Evidence | Finding |
| --- | --- |
| `evt_000002`, `evt_000030` | The user required a playable flat slice in a real browser before full terrain and lighting. The committed plan moved seeded terrain into step 1, lighting/meshing/physics/full Node tests into steps 2–5, and browser work into steps 6–7. No browser or server tool ran. |
| `evt_000189`, `evt_000213`, `evt_000244`–`evt_000250` | The then-existing world tests passed, including 46 assertions across determinism, persistence and fixtures. The committed step-1 check passed; shared-file work then correctly marked that evidence `needs_reverify`. It was still awaiting recheck at handback. |
| `evt_000305`, `evt_000310`, `evt_000307`, `evt_000312` | Two empty, length-stopped generations consumed about 6m45s and 36,864 completion tokens without an answer or tool call. The existing bounded fallback lowered thinking from medium to low to off. The next response wrote `lighting.js` at `evt_000317`. |
| `evt_000458`, `evt_000491`, `evt_000515`, `evt_000615` | Four manual Node checks piped through `tail` returned tool success despite runtime errors. The pipeline's final command masked the failed Node exit. Later harness checks retained the actual failures. |
| `evt_000195`–`evt_000197`, `evt_000203` | The native script runner required exactly one script and offered no script-argument field for selecting groups. Passing the script twice was rejected; group-specific checks subsequently used shell commands. |
| `evt_000530`, `evt_000544`, `evt_000596`–`evt_000599`, `evt_000634` | Initial flushes failed because a facade light entry was missing. A repair added a lighting rebuild inside `loadChunk`, producing `loadChunk → rebuildRegion → loadChunk` recursion. |
| `evt_000651`–`evt_000654`, `evt_000681`–`evt_000684`, `evt_000691`, `evt_000725` | Splitting block loading removed recursion. The eleven-cell skylight assertion passed, but closing the shaft caused the second flush to fail at `world.js:332`: `light.get(c).sky` accessed an absent entry. The final check repeated that failure on unchanged source. |

The remaining lighting bug is a state-lifecycle mismatch. `lighting.js:86` loads missing ring chunks through the facade's block-only loader. That loader inserts entries into the world's `chunks` map without creating corresponding facade light entries. `flushUpdates()` snapshots chunk keys, then each rebuild can load more ring chunks. The next flush includes these newly loaded chunks and assumes their facade light arrays exist. It also performs full rebuilds during ordinary edit settling, conflicting with the incremental-edit contract. Adding a missing-value guard alone would not establish correct lifecycle or lighting behavior. The final recorded `world.js` SHA-256 is `33323f0fc44fc04214364a00ab816dd7caae3f00066402a735010c33b3b5de4c`.

The reads were legitimate navigation: 24 successful `read` receipts contained 65,531 source bytes with **zero overlapping byte ranges for the same path and SHA**. Four continuation offsets were supplied and followed correctly (`78→86`, `269→277→285`, `293→301`). Recovery used 11 reads and four searches, mostly focused on new regions or changed versions. No `recovery_read` call occurred, so stale historical-source retrieval is not an observed cause here.

Recovery stopped at its 16-iteration cap. It recorded two failed checks after source changes (`evt_000636`, `evt_000693`), not three failed repair/check cycles. A changed error was retained as evidence, not promoted to a pass.

## Changes

- **Failure context before the next model call:** `internal/loop/recovery_focus.go` extracts at most three separated trace locations from declared source files, reads a small current region through the normal registry and workspace jail, and preserves the complete bounded read receipt in the journal. Cache identity includes path, line and source SHA. Pinned display is capped at 6,000 bytes and further limited on small model contexts; omitted source is explicitly labelled, with event references for retrieval. A new failure replaces the prior pinned focus, including when no window manager is configured. The context asks the model to trace state creation, updates and removal before repairing the symptom; it performs no generated source repair.
- **Recall wired to failed checks:** the focus path calls `OnErrorWithStatus` with a bounded timeout, excludes already-present evidence, and records a `recovery_recall` note with backend/degraded status, qualified references and separate retrieved/injected counts. Retrieved history is advisory and earns no repair credit. Bounded excerpts retain recognizable diagnostic lines and nearby frames rather than burying errors under long command preludes.
- **Local evidence takes precedence:** `internal/memory/recall.go` searches a bounded suffix of the current session journal before indexed results: at most 512 event lines, 4 MiB total and 128 KiB per event. Indexed fallback checks session/document identity, filters result-derived types, deduplicates original evidence references, and falls back from hybrid to lexical search. Index outages retain available local evidence. Output remains at most five bounded excerpts with provenance.
- **Typed observation indexing:** `internal/memory/indexer.go` indexes recovery observations only when their referenced tool result agrees on tool, outcome and output hash. Captured source hashes require supporting read receipts. Arbitrary typed notes, raw reasoning and recursively recalled content are excluded from this evidence path. Historical tool success is not current correctness.
- **Native group checks:** `run_checks` accepts `script_args` for `node_script`, appended literally after the single script path and included in check identity. Limits are 32 nonempty UTF-8 arguments, each at most 1,024 bytes without NUL. They cannot become Node runtime flags or shell commands; read-only/no-network execution and unknown assertion-count semantics remain intact. For example: `{"runner":"node_script","paths":["tests.mjs"],"script_args":["skylight"]}`.
- **Accurate pipeline status:** the bash tool uses `bash -o pipefail -c` in both ordinary and recovery execution. A failing check piped through `tail` remains failed. This does not add implicit `errexit` or override explicit shell failure handling.
- **Planning guidance:** `commit_plan` now explicitly requires preserving requested delivery order and early runnable/browser milestones, and asks for repeated-operation checks around shared state. The step-prompt comment now accurately states that original user constraints remain beside the active step.

## Limits

Original task text was already pinned in step context; the observed browser-order failure happened in planning, not proven context erasure. The new ordering guidance is prompt-only, not a semantic plan validator or an automatic rewrite of existing plans.

Existing memory-index cursors are not reset and historical documents are not bulk reindexed. Local error recall can use bounded recent journal evidence independently; older evidence may still depend on its existing indexed form. Excerpts and source identities do not certify that historical evidence matches current files.

Reasoning-token budgeting remains deferred. The model client adds reasoning allowance to the overall output cap, and the turn token guard subtracts recorded reasoning tokens. The two empty generations are evidence of substantial cost, not proof that increasing a budget would solve implementation errors.

## Verification

Targeted tests reported during implementation, using `GOCACHE=/tmp/cerveau-native-check-gocache`:

| Command | Observed result |
| --- | --- |
| `go test ./internal/tools -run '^(TestBash\|TestRecoveryShell\|TestRunChecks)' -count=1` | PASS, 2.831s. Covers pipeline status and bounded literal script arguments, including a real Node fixture. |
| `go test -race ./internal/memory` | PASS, 2.208s. Covers local-first recall, backend fallback/timeouts, scope and provenance validation, bounds and indexing. |
| `go test ./internal/loop -run 'TestRecovery(Focus\|Locations\|FirstModelCall\|NativeDebug)' -count=1` | PASS, 0.040s. Covers declared-path confinement, source-version reuse and failure context before model execution. |

After the final focus-size and dropped-context fixes, `go test ./internal/loop ./internal/tools ./internal/memory -run 'TestRecovery|TestCompressWithoutWindow|TestPlan|TestBash|TestRunChecks|TestOnError' -count=1` passed (loop 1.000s, tools 2.847s, memory 0.003s). The dedicated memory race suite above covers retrieval-specific tests outside this name filter.

The first full release gate passed Go race tests, Go vet, 101 frontend tests, frontend build and CSS checks. Svelte-check reported zero errors and eight existing warnings. Five browser fixtures initially skipped because the shell lacked runtime environment variables; a separate configured rerun passed all 13 browser tests with zero skips. This first bundle is superseded by the final diagnostic-excerpt refinement and is not to be installed.

### Actual services and journal replay

The configured Nemotron embedder returned one real 2048-dimensional numeric vector. Typesense's existing lexical `skylight` search returned 20 results, including this benchmark's failed tool results. These prove runtime availability, not semantic retrieval quality; no embedding-prefix or index-schema migration was attempted.

The new local `OnErrorWithStatus` was run read-only against the actual session, excluding the current pinned check `evt_000725`. In 8 ms it returned five pulls (`backend=local`, `degraded=false`) with qualified original references `evt_000691`, `evt_000544`, `evt_000530`, `evt_000491`, and `evt_000458`. After the excerpt refinement, the exact missing-`sky` TypeError and nearby `world.js:332:29`/`:312:29` frames survive; earlier null-index errors retain `lighting.js:306:27`. Historical tool-success flags from the flawed pipelines remain labelled as recorded, not reclassified as test passes. Probe command: `GOCACHE=/tmp/cerveau-native-check-gocache go run ./build/memory-recovery-probe` (ignored, read-only helper).

### Final release and installation

Final command:

```sh
CERVEAU_PLAYWRIGHT_MODULE=/home/shiny/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright/index.mjs \
CERVEAU_CHROMIUM=/home/shiny/.cache/ms-playwright/chromium-1223/chrome-linux64/chrome \
GOCACHE=/tmp/cerveau-native-check-gocache node scripts/build-release.mjs
```

PASS: 5 release/safety tests, 13 native-browser fixtures (zero skips), 101 frontend tests, Svelte-check (zero errors; eight existing warnings), production frontend build, 3 production-CSS checks, full `go test -race -timeout 90s ./...`, `go vet ./...`, `git diff --check`, binary build and artifact checksums. Final affected race packages: loop 12.468s, memory 2.235s, tools 21.634s. Existing frontend bundle-size warnings remain.

- Installed bundle: `build/cerveau-0.6.0-alpha-LABRIG-sulDnj`.
- Live revision: `a3a8367-dirty-b48d213f77b3`.
- Verified backup/install receipt: `/home/shiny/.crv/install-backup-harness-recovery-AfVOqh/install.json`. Previous binaries remain recoverable in `old/` and `retired/`; no data deleted.
- No active runs before stop or after restart. Only `cerveau.service` restarted, to MainPID 3558203; managed Typesense followed it. Core, embedder and Core-proxy PIDs remained 3404579, 3404578 and 3407038 respectively.
- Model, embedder and Typesense health checks passed. Startup tool-call canary passed at 05:48:59 local log time. Native Reflex loading reports 21 entries and no load errors.
- Installed binary checksums and live build identity match the bundle. Protected config, service definitions, installed Reflex packs/helpers, previous Minecraft files/journal and current MineKraft source/test/DGV files/journal matched before and after. Medium thinking and Default sampling were preserved.
- Existing Ignite/legacy-backend startup issues remain deferred; those intentionally inactive services were not started or changed.

The benchmark is still paused and has not been repaired or rerun by this change. Resume is a harness-assisted continuation, not a fresh benchmark or proof of a one-run result. No benchmark acceptance result is claimed.
