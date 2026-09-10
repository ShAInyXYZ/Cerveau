# Minecraft benchmark: harness recovery

## Scope

Initial operator request: fix the harness so Cerveau can repair its own project, without installing or restarting it in that implementation turn. A later explicit request authorized installation; the local installation receipt is retained privately. No direct Minecraft code changes, session commands, automatic retry or Core retuning. The long-composer UI issue remains deferred in docs-private/tobefixed.md.

## Session evidence

Read-only audit of `~/.crv/sessions/20260905-160857-minecraft/events.jsonl`, run `a5b486ca7522a689f99c89ba`, 2026-09-05 16:30:32–17:15:13 UTC:

- 75 model responses with usage, 86 tool call/result pairs; maximum input reported by the model endpoint: 55,484 tokens. Default sampling, Medium autopilot thinking. Final stop: plan_blocked, not context overflow.
- `evt_000019` and `evt_000022`: dgv-context and dgv-catalog rejected as not offered during planning.
- `evt_000030`: step 1 claims storage/generation/edits/lifecycle; its committed check only compares one generated chunk between fresh worlds. A passing check cannot prove the wider claim.
- `evt_000399`: failed patch's nearest-source hint includes literal `336\t` but calls it line 161. `evt_000582`: another calls a literal `743\t` line 8. ApplyPatch was validating against numbered, cursor-dependent, capped public Read output rather than raw source.
- `evt_000541`: the fresh repair attempt's first read continues at character 24,000, although its model window does not contain the previous attempt's reads.
- StepPrompt used Verify.Describe, a clipped human-readable label, to instruct execution. Commands longer than the label limit lost their assertions in the step/retry prompt; the verifier itself still executed the complete stored command.
- `evt_000595`: real generated-code defect remains: lightAt → computeLightAt → neighboring lightAt recursively evaluates unloaded cells without a completed cached value, overflowing the stack. This is not fixed by the harness changes.

Audit snapshot hashes (not backups): events.jsonl `5fcb41736b2e26d04833271637de27b6197a8744e672b53d758c49da4e9fdca2`; world.js `edd61f3a9165d617e094da55c7b5e92af6de2b2a300afeb6331e4a22c3910ac9`.

## Design and implementation

### Bounded DGV access during planning

Allow dgv-list, dgv-catalog, dgv-context, dgv-read and dgv-check only when registered as safe and already offered by the active mode. They share the existing bounded planning-read budget (four model responses containing non-orientation reads, not a four-tool-call maximum). No wildcard allowance for Reflex tools, no DGV create/update, and no admission of arbitrary tools just because they are labeled safe. After the budget, commit_plan remains forced. The same offered set controls dispatch and its journal result.

This policy relies on the trusted installed pack implementing its declared operations. It is not an OS-level read-only sandbox for malicious executables mislabeled as DGV tools.

### Authoritative patch inputs

Patch prevalidation reads complete bounded raw source through the registry and the built-in Read jail. A private context marker selects that internal path; it is not a model-facing raw-read option. Public read formatting, pagination and offsets are excluded from matching. Oversized sources fail explicitly instead of validating against a truncated slice. Patching does not advance public read cursors.

Each hunk must explicitly supply path, old_string and new_string. Missing or null strings cannot silently become overwrite/deletion requests. Empty strings remain explicit operations. Malformed-call errors explain the accepted structure; mismatches give bounded current-source evidence without changing files or guessing a match. Runtime write failures must disclose partial progress: multiple file writes are not a filesystem transaction.

Prevalidation simulates hunks in order against in-memory source, using the same transformation as Edit. Dependent same-file edits work; later conflicting edits fail before earlier hunks write anything. Resolved aliases share the simulated source. Indentation-only matching requires a unique match, never the first of several candidates. Direct Edit also requires an explicit replacement string to delete text.

### Read state belongs to its model window

WithFreshReadCursor gives each chat run and each step/retry its own continuation state. Repeated reads within that scope still advance; a fresh scope starts from the beginning. Context-local state avoids resetting shared tool pointers or disrupting another run's cursor. Explicit line ranges and offsets remain available.

### Full checks and honest coverage guidance

StepPrompt sends full serialized Verify JSON, including the complete command/expression. Planning and execution guidance asks for checks covering all behavior claimed by the step, or splitting the work into narrower steps. Final integration/browser checks are additional evidence, not permission to defer proof of earlier behavior. Earlier steps are described as having passed their declared checks, not as universal proof of correctness.

This is guidance, not a semantic test-coverage validator. Existing stored plans and the verifier's pass/fail authority are unchanged. A weak old check is not automatically upgraded by installing a new harness.

## Regression coverage

Tests were added before implementation to reproduce the faults. They cover planning DGV offer/dispatch agreement and read-budget closure; denied write/risk/mode variants; raw multiline and beyond-page patch matching; malformed hunk no-write behavior; raw-read cursor independence; independent read scopes; fresh retry reads from the beginning; exact long command/expression preservation; and check-coverage guidance.

No iteration, token, error, timeout or retry limits were increased. Suspended-run resume, revisions and fail-closed verification remain subject to existing tests.

## Validation and handoff

- Full `go test -race -timeout 90s ./...`: passed.
- `go vet ./...`: passed.
- Frontend: all 71 Vitest tests passed; Svelte checking reports zero errors and 16 existing warnings.
- Release identity / QA safety scripts: four tests passed.
- End-to-end fixture: scripted model reads a region beyond the first page, applies a multiline patch through the real dispatcher, and passes its declared check on its first attempt. Source bytes outside the requested change remain identical. This is not a real-Core quality benchmark.
- Independent read-only review found no blocking issue in planning fences, cursor isolation or raw patch authorization.
- DGV run-stepwise, loop-contract and rfx-capability-roadmap affected nodes updated; no lint errors or warnings (existing informational diagnostics retained).

Build-only handoff: `build/cerveau-0.6.0-alpha-LABRIG-V7Bqg4`, revision `a3a8367-dirty-d706d2b3a3ab`. The release script reran all gates, built both binaries and the panel, verified embedded Planner 1.5.0 identity, and checked SHA256SUMS. An independent post-build recomputation matched source SHA-256 `d706d2b3a3abf246d52c179d0f478b477a831260c10d27b248f1dda88b0e3234` before this handoff paragraph was appended. No application source changed afterward. Vite also reports the existing large-bundle warning.

The first bundle, `build/cerveau-0.6.0-alpha-LABRIG-Sap9VK`, is retained with DO-NOT-INSTALL.md: a final source edit overlapped its packaging and the independent fingerprint check caught the mismatch. Only V7Bqg4 is the handoff build.

At the build-only handoff, the installed revision remained `a3a8367-dirty-5e683af76a37`, with Default sampling and unchanged session/world.js hashes. The later operator-authorized installation now serves `a3a8367-dirty-d706d2b3a3ab`; see the installation record above. No successful Minecraft repair is claimed. This build includes the previously uninstalled Default-sampling fallback and tooltip corrections; it does not fix the deferred composer layout.

After operator installation, resume the existing session with the lighting diagnosis and an instruction to recheck storage/persistence plus shaft, border, source-removal and enclosure lighting. Let Cerveau make and test the repair. Attribute that later run as a harness-assisted continuation of this failed benchmark, not a fresh unassisted pass. Do not mark remaining rendering/physics/browser work successful until it is actually exercised.
