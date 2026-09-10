# Native Reflex follow-up for 0.6 LABRIG

Date: 2026-09-05. Source implementation and build-only handoff; not an installation record. Existing installed packs, binaries, services and Brain Core settings were not changed by this work.

Subsequent explicit installation request: completed and verified separately; the local installation receipt is retained privately. The development validation below remains distinct from live operator acceptance.

## What ships

| Surface | Capability | Limits and reference |
| --- | --- | --- |
| DGV 0.1.0 | Seven native talents: catalog, list, focused context, exact read, check, create, guarded update. Reuses the original DGV engine directly, without MCP. | Source fingerprints are scoped observations, not semantic proof. [Conversion reference](rfx-dgv-conversion.md). |
| GitHub 2.0.0 | Eleven repository-bound talents; structured status/diff/history, explicit staging, staged-only commit, local profile/init, account inspection, approved push/publish. | Breaking migration from the installed 1.x scripts. No hidden model call or account switching. [Migration and restrictions](rfx-github.md). |
| DevCheck 0.1.0 | Local DOM/console/network facts, declared element checks and JPEG screenshots with retained receipts. | New isolated browser context; no existing profiles, arbitrary JavaScript, external resources or mutating HTTP methods. [Operator guide](rfx-devcheck.md). |
| Chat and RFX dock | Content-aware scrolling, natural composer layout, typed All actions forms, mobile pack rail, source-specific incidents, image preview/crop/attach. | Existing palette, typography and controls retained. Impeccable guided the bounded refinement; no new brand system was introduced. |

DGV is the first documented MCP-to-Reflex conversion. The repeatable pattern is: preserve the expert engine, replace the protocol surface with a few bounded procedures, emit complete typed receipts, and give the user controls over those same procedures. No model invocation is hidden inside these three helpers. This release does **not** claim measured token, latency, temperature or throughput savings.

## Diagnosed and corrected

| Finding | Correction / evidence |
| --- | --- |
| Nested argument validation checked only outer types. | Required members, arrays/items, string and numeric bounds, patterns and supported unions checked recursively. Enum matches cannot bypass other constraints. Open nested DGV objects remain open for upstream validation. |
| Nested Reflexes could shed parent restrictions. | File/network primitive restrictions checked at final dispatch after repairs; nested exec requires each parent's subprocess permission and intersects environment allowances. |
| Ambient approval metadata could be inherited by an exec helper. | Reserved `CRV_RFX_*` environment names cannot be allowlisted or inherited; approved context alone adds the human-approval flag. |
| External output was retained in unbounded buffers. | Drain stdout/stderr with 1 MiB retention caps each; exceeding either is an explicit failure, not silent success. Native packs additionally budget complete serialized JSON below their ingress caps. |
| Manual Git failures after a successful plan offered “Retry unfinished step.” | Manual run kind/name are journaled from admission through termination; UI points to the Reflex and cannot retry the unrelated plan/message. |
| Status widgets could start disabled, unsafe or overlapping actions. | Safe, enabled, argument-free, visible, open and idle-only polling; shared per-session request leases and stale-response rejection. New packs use explicit buttons. |
| Stream reserved 400 px desktop / 300 px mobile for a composer already in normal flow. | Bottom padding is 24 / 12 px. ResizeObserver follows growing content only while at the tail; reading older content is not interrupted. |
| Attach control advertised modalities without an implemented image route. | Explicit preview/crop, validated command images, persisted/replayed context and visible unsupported/unknown capability states. Audio/video attachment remains unimplemented. |
| Raw event writes could inject invalid images, and replay decoded all historical rasters before trimming. | Event image validation matches command admission; retained history uses a bounded validation cache after context selection. Unavailable historical images are disclosed without poisoning unrelated future turns. |
| DGV's failed check could exit successfully, or an error's JSON escaping could exceed ingress. | Failed checks retain diagnostics and exit nonzero. Encoded error envelopes are bounded, including hostile control/HTML text. |
| DGV inventory hit the cap on this repository's ignored reference documentation. | Bounded Git-aware inventory honors existing ignore rules, with explicit scope; non-Git workspaces retain bounded traversal. Actual Cerveau context was exercised read-only. |
| Git staging rejected tracked deletions when the parent directory was removed. | Existing-ancestor containment and exact tracked-path validation, covered by nested-deletion fixtures. |
| Blind “fuzz all repository packs” executed external programs. | Loader/manifest contract gate retained; pipelines keep stubbed fuzzing. External packs use their isolated domain fixtures, not arbitrary invocation on installation. The explicit CLI fuzz command still runs exec programs for real and is not a sandbox. |

Permission cards and scrubbing are not an OS sandbox. Trusted exec code can access resources with the operator's process permissions. The fixed procedures add their own path/network fences; this does not make arbitrary user-created programs untrusted-code-safe. PATH prioritizes the host executable's directory for operator-installed sibling helpers, never a session or pack directory.

## Visual context contract

The attachment button opens a preview dialog. A user may choose PNG/JPEG/WebP or invoke the browser's window/tab/screen consent picker. Capture takes one frame and stops all tracks before editing. Crop coordinates refer to original source pixels; compression happens **after** cropping, so a small selected region retains detail. Sources are limited to 20 MiB for uploaded files and 64 megapixels / 16384 px per axis before canvas use; the browser's initial image decode still has its own memory cost.

At most two images are sent per chat command. Each must be inline PNG or JPEG, at most 256 KiB and 1280 px per axis. The backend decodes the raster and derives dimensions, MIME and SHA256; supplied metadata is not authority. Remote URLs, SVG, corrupt rasters and oversized payloads are rejected. Image-only chat is allowed. Steering is text-only; attached images remain for the next message.

DevCheck's screenshot receipt can be attached explicitly from its result card. The host reads only `.devcheck/` relative to that session workspace, checks the expected SHA256 and rejects symlinks/escapes. This route uses Linux `openat2`; unsupported platforms fail closed. Merely capturing or attaching does not send a message.

Images survive journal replay, edited-message resend, message retry and the original plan's step context. Text-only requests preserve their original wire shape; visual requests use text plus `image_url` content parts. Context admission reserves a conservative 4096 tokens per image and does not tokenize base64 as prose. This is an estimate, **not measured Core vision token accounting**. Absent/failed modality probes are unknown, not evidence of a text-only model.

## Verification record

- Full Go race suite and vet passed during integration; final build runs them again.
- Frontend: 71 tests, including refusal to render remote/SVG sources from imported image history; Svelte check has zero errors and 16 pre-existing warnings. Four stale RFX error styles were removed, accounting for the reduction from the previous 20 warnings.
- DevCheck: 12 Node tests with actual fixture Chromium, including local-origin/method enforcement, redirected escapes, byte limits, unambiguous checks, screenshot bounds and complete JSON under adversarial text.
- DGV: original-engine fixtures, actual CLI→registry→exec checks, retained backups, stale hashes, invalid graphs, failed-check exit status and hostile error envelopes. Packaged helper successfully read Cerveau's focused `run-stepwise/stepapi` context (11 mapped files). `loop-contract` drift scanned 445 files, reported 34 unclaimed directories, and correctly failed; no claim that the whole repo is fully mapped.
- Browser acceptance at 1440×1000 and 390×844: stream growth, retained scroll position, manual Reflex identity, no mount-time actions, textarea growth, IME Enter, simulated capture stopping, crop-before-compression, bounded JPEG, image command, typed All actions, invalid JSON refusal, changed-argument confirmation invalidation and no horizontal overflow.
- Visual batch: `build/rfx-ui-acceptance-7Kgms2/` (four screenshots and results). Extended functional confirmation without extra screenshots: `build/rfx-ui-acceptance-qaXhtf/results.json`. Baseline: `build/rfx-ui-check-rVn8QYq5/`.

The browser acceptance API is mocked and display sharing is simulated. It proves frontend behavior, not actual desktop permissions or live model interpretation. Backend fixture tests independently cover the real image API/message path. These evidence levels are intentionally separate.

## Build and operator acceptance

`node scripts/build-release.mjs` builds the host with its embedded Planner. `node scripts/build-rfx-packs.mjs` builds an independent native pack bundle, retaining the exact original DGV engine/dependency bytes, licenses, provenance and checksums. The bundle requires an already installed Node/Git runtime and an explicitly configured existing Playwright/Chromium runtime for DevCheck. Neither builder installs or restarts anything. See the conversion guide's verified-copy operator procedure.

Still unverified: live Core image consumption and its exact vision budget; real desktop permission/capture behavior on the operator's browser; authenticated GitHub publish/push and credential-helper configuration; DevCheck compatibility with real applications needing APIs, external assets or authentication. Existing service-worker-heavy or mutating applications may be intentionally incomplete under DevCheck's read-only fence. Model selection of the new talents and end-to-end token savings need their own benchmark.

Architecture and status are recorded in `dgv/rfx-capability-roadmap.dgv.json`, `dgv/loop-contract.dgv.json` and `dgv/run-stepwise.dgv.json`. A DGV node marked implemented does not make any of the above live acceptance cases verified.
