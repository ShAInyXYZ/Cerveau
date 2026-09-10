# DGV: the first MCP-to-Reflex conversion

Status: reference pack `dgv` 0.1.0; native helper and original-engine fixture tests implemented. Installation and live model acceptance are separate evidence, not implied by unit tests.

## Preserve the engine; replace the integration surface

The previous DGV integration exposes authoring operations through an MCP server. This pack starts one short-lived `rfx-dgv` process per invocation. It consumes one JSON object on stdin and emits one bounded JSON result. No MCP SDK, protocol session, service, viewer server, network request or model call is involved.

The original `@dgv/core` remains the sole owner of graph vocabulary, schema validation, semantic lint, drift matching, layout, patch semantics and history. A small embedded JavaScript adapter imports that engine directly. Go owns workspace file access, argument limits, source hashing and guarded writes. This is not a Go rewrite of DGV.

The RFX value is a smaller set of workflow verbs, fixed defaults and observable results, plus a human cockpit invoking the same talents. This conversion does not claim measured token or latency savings yet; compare identical workloads before publishing such numbers.

## MCP-to-native mapping

| Previous operation | Reflex | Deliberate compression |
| --- | --- | --- |
| `dgv_list` | `dgv-list` | Workspace-only graphs, bounded entries, content identities. |
| `dgv_catalog` | `dgv-catalog` | Exact upstream vocabulary; no second kind list. |
| `dgv_read`, summary export | `dgv-context`, `dgv-read` | Bounded overview or one exact element, never dump every graph/history into context. |
| `dgv_lint`, `dgv_drift` | `dgv-check` | One `checks` enum: lint, drift or all; upstream diagnostic codes retained. |
| `dgv_create`, `dgv_apply`, `dgv_layout` | `dgv-create`, `dgv-update` | New-only create or explicit upserts with expected hash; validate, layout and record history automatically. |
| Viewer launch, delete, arbitrary export, dedicated flag/history tools | Not exposed in v0.1 | No hidden service launches or deletion. Flags/notes/status may be explicit element upserts; exact element reads preserve flags. |

The graph is an architecture aid, not the executable Cerveau plan. Updating a node to `done` does not submit a run, verify an artifact, or mark a planner step passed. Advanced graphs can be built in several bounded upserts; the pack never introduces an executor inside the diagram or cockpit.

## Guarantees and explicit limits

- Working directory is the active session workspace. Graph names map only to `dgv/<name>.dgv.json`. Go `os.Root` confines file operations; names reject traversal and absolute paths. Graph listing rejects symlinks, and source inventory never follows them. This protects these helper operations, not arbitrary third-party executables or hostile bind mounts.
- Inputs are capped at 256 KiB; graph files at 2 MiB; writes at 64 total supplied graph elements; result JSON at 24 KiB. Context defaults to 12 nodes, maximum 30, with explicit truncation counts. Oversized exact reads fail with `output_limit` rather than returning broken JSON.
- Inventory first uses read-only `git ls-files --cached --others --exclude-standard -z`, bounded to 2 MiB of names and 10,000 regular files, respecting repository ignore rules. Git fsmonitor, hooks, optional locks and system/global config are disabled; Git supplies names only and final file checks stay under `os.Root`. Non-Git workspaces fall back to a walk capped at 30,000 entries without `.gitignore` interpretation. Both exclude DGV, dependency, build and cache directories and report the chosen scope. Drift coverage is explicitly scoped, not an exhaustive claim about the whole repository.
- Context hashes current graph bytes and files mapped to the returned node slice, at most 8 MiB per file and 32 MiB aggregate. It never returns file contents. The first observation is `unbased`. `unchanged` means equal to a supplied fingerprint for that exact slice. Missing, unmapped or unreadable files give `incomplete`; changed bytes give `changed`. Either requires targeted source inspection. Hash reads are sequential, not an atomic snapshot of a repository being edited.
- `graph_sha256` is the raw graph identity for optimistic updates. `freshness.fingerprint` is a different identity for a graph/source observation. Neither certifies semantic truth, code behavior or a successful implementation.
- Create uses atomic create-if-absent and never overwrites. Update requires the latest graph hash, validates before writing, preserves an exact content-addressed backup under `dgv/.rfx-backups/`, verifies that backup, writes a synced temporary file, rechecks the current hash and replaces atomically. Original-engine history records `by: rfx`. Successful temporary cleanup happens only after verifying the new graph copy; failed prepared files remain available for inspection.
- RFX mutations use per-graph advisory locks. The external DGV editor/viewer does not share those locks. A hash recheck detects an intervening external edit, but there is no kernel compare-and-swap against a concurrent external rename. Avoid simultaneous editor saves during an RFX mutation. Backups preserve the observed prior bytes.
- No remove, reset, restore, shell, arbitrary engine path, arbitrary workspace parameter, or history replacement is model-callable. Create/update are sensitive, autopilot-only talents and remain subject to host policy. Read-only talents are available in discussion.
- The helper launches Node with a scrubbed environment and a 10-second engine deadline; Git inventory has a five-second deadline and the CLI deadline is 20 seconds. Stdin, stdout and stderr are bounded. Error envelopes retain a stable code, at most 2,048 UTF-8 bytes of message and an explicit truncation flag; bounds hold after JSON escaping. Dependency installation is never an action of the pack.

## Packaging contract

`rfx-dgv` resolves the original engine from the trusted operator variable `RFX_DGV_CORE` (absolute path), otherwise from:

```text
<helper-directory>/dgv-engine/packages/core/src/index.js
```

Build the helper with `go build -o <output>/bin/rfx-dgv ./cmd/rfx-dgv`. Copy the **unchanged** upstream `packages/core/package.json` and `packages/core/src/` plus upstream root `package.json` and `LICENSE` under `bin/dgv-engine/`. Copy exact `node_modules/@dagrejs/dagre/` and `node_modules/@dagrejs/graphlib/` package trees under that engine directory, retaining their license files. Record upstream revision and SHA256 identities of every copied file. Do not edit those copies as an alternate engine implementation.

The inspected upstream revision was `864bf708dbacdf97c4a555c7cf617aeeb72b5593`, `@dgv/core` 0.2.0, `@dagrejs/dagre` 3.1.1 and `@dagrejs/graphlib` 4.0.5. These are inspection evidence, not hardcoded promises about later bundles; bundle provenance must describe the actual copied bytes. The MCP package, MCP SDK, viewer and server are not needed. A compatible locally installed Node runtime is required.

Manifest argv is `/usr/bin/env rfx-dgv <verb>`. The host's helper-directory PATH precedence is part of installation; the pack itself contains no machine-specific path. An operator may explicitly use `RFX_DGV_CORE` for development, but it is not a model parameter and an override changes the trusted engine identity.

### Build-only bundle

Run `node --test scripts/build-rfx-packs.test.mjs`, then `node scripts/build-rfx-packs.mjs /absolute/path/to/Dia-GramV`. The script creates a new `build/rfx-native-*` directory every time. It compiles `bin/rfx-dgv` and `bin/rfx-github`, copies the exact engine/dependencies beside the helper, includes `bin/devcheck/` and its internal `bin/rfx-devcheck` launcher, and copies canonical `rfx/dgv`, `rfx/github` and `rfx/devcheck` without test files. Nothing is installed.

The script refuses source symlinks, nonregular files, missing dependencies or missing licenses. It uses the locally installed Go toolchain or the exact already-cached version required by `go.mod`, with module/toolchain downloads disabled. A missing toolchain/dependency is an operator prerequisite, not permission to download it. Copies are hash-verified; `provenance.json` identifies upstream revision, dirty state, dependency versions and copied source hashes. Its `cerveau.go_source` also records an aggregate SHA256 and per-file hashes for every Git-visible repository Go file (including tests), `go.mod`, `go.sum` and the embedded DGV bridge. This deliberately broad source identity covers dirty/untracked helper inputs instead of claiming HEAD alone identifies them. The builder repeats that inventory after compilation and refuses the bundle if its identity changed. The scope is not a minimal dependency closure and does not hash the installed Go standard library/toolchain bytes; its version is recorded separately. `inventory.json` records regular-file hashes and the exact intentional DevCheck symlink target. `SHA256SUMS` covers regular files and the inventory; verify both checksums and the symlink inventory before installation.

Node must be on the host's trusted PATH. DevCheck additionally needs an existing Playwright installation and Chromium. Configure `CERVEAU_PLAYWRIGHT_MODULE` with the absolute Playwright module entry path and `CERVEAU_CHROMIUM` with the browser executable path when they are not already discoverable. These are operator configuration values, not model arguments. Browser binaries, Playwright, Node and Git/GitHub authentication are not downloaded, copied from private profiles, or configured by the bundle builder.

Installation remains operator-owned: verify bundle hashes; inventory the exact installed helpers/pack destinations; copy existing versions into a new backup directory and verify byte-for-byte copies before replacement; then install the helper set with adjacent `dgv-engine/` and `devcheck/` trees and the three canonical packs. Never merge an upgraded pack into an older folder blindly: obsolete talents could remain callable. Use a verified, whole-pack replacement with retained backups. Preserve unrelated packs and `.state.json`; keep the original installed DGV/MCP software untouched. The builder does not restart Cerveau, touch Brain Core, or perform this installation.

## Tests and evidence

Tests were written before implementation and initially failed because the helper did not exist. `go test -race ./internal/rfxdgv ./cmd/rfx-dgv` exercises the actual original core when it is available, using only fresh temporary workspaces:

- Invalid names, oversized and non-object arguments, unknown fields/actions.
- Escaping graph-directory symlinks and incomplete/missing source coverage.
- Native create, catalog loading through manifest validation, list, focused context and check.
- Same-path source-content changes, supplied observation comparison and unbased first reads.
- Upstream history, verified exact backups, stale-hash rejection and new-only create.
- Forbidden removal/history changes, malformed kinds and dangling-edge rejection without modifying the graph.
- Actual manifest → registry → exec → CLI composition, with discussion-mode read access and mutation refusal. Negative drift checks emit one complete result and exit nonzero so the host records a failed check rather than a green tool result.
- A Git-ignored reference tree containing 10,001 files cannot exhaust the code inventory. Error strings containing large amounts of HTML/control characters and multibyte text stay bounded as serialized JSON.

Original-engine integration tests look for `RFX_DGV_CORE` or a sibling Dia-GramV checkout. They explicitly skip when unavailable; that skip is not proof of engine integration. Pure argument and jail tests do not skip. Production builds must run with the packaged or original engine present. No model inference, Core retuning or service restart is required for these tests.

An interim build-only bundle at `build/rfx-native-KvYGT8` passed all 188 regular-file checksum entries and a packaged-helper smoke without an engine override: upstream catalog (16 node kinds), new graph creation, exact element read and lint. Its temporary workspace was `/tmp/dgv-bundle-smoke-cApARb`. This was a packaging smoke, not live model or UI acceptance; rebuild after subsequent source edits, including the negative-check exit-code correction. Failed build attempts were retained and are not release artifacts.

The subsequent `build/rfx-native-FSTcea` bundle was tested read-only against the actual Cerveau workspace after the acceptance corrections. `dgv-context` for `run-stepwise`, focus `stepapi`, succeeded and hashed 11 mapped files with state `unbased`. `dgv-check` for `loop-contract` with drift scanned 445 Git-visible files and reported 18 mapped nodes, zero missing paths, 34 unclaimed directories and one unmapped node. It correctly exited 1 with the complete structured findings. This is evidence of honest scoped drift reporting, not a claim that the architecture map has no remaining drift.

## Reusable conversion recipe

1. Identify which library implements the capability independently of MCP. Reuse that maintained engine/API directly; do not wrap the MCP server under a new name.
2. Map user verbs into roughly five to ten bounded talents. Fix routine flags and ordering in code, expose few typed slots, and retain explicit mutation boundaries.
3. Put filesystem, timeout, retry and output guarantees in the adapter where they can actually be enforced. A permission card alone is not a subprocess sandbox.
4. Preserve stable domain diagnostics and structured receipts. Separate successful inspection from a clean inspection result; failed checks must stay visible.
5. Tie summarized context to source identity and scope. Never turn a cached summary into an unsupported truth claim.
6. Give humans a cockpit over the same talents. Keep presentation separate from capability, with no second executor or hidden model loop.
7. Write fixture safety/behavior tests first, package exact engine provenance and operator requirements, then record live acceptance separately. Only after measurements claim resource or token improvements.
