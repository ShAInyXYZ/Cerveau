# Core-managed sampling default — 2026-09-05

## Decision and evidence

At the operator's request, Default is the default: omit temperature and top_p overrides so the served Core supplies its configured values. This does not prove that those values are optimal, inspect their effective values, or retune the Core. Explicit Strict, Neutral and Creative remain available.

The historical comparison in docs-private/benchmark-harness-bugs.md changed harness code and sampling together. It cannot establish Strict as best for code. W8A16 control acceptance used Strict but did not compare sampling quality. Removed contrary claims and model-specific assumed values from sampling source comments and the two UI tooltips; Impeccable's clarify guidance was applied only to this wording, without visual changes.

## Applied live

- Before: GET http://localhost:7700/api/sampling returned active=strict.
- POST /api/sampling with {"name":"default"} returned active=default.
- A subsequent GET returned active=default; the sampling field in ~/.config/cerveau/config.json was independently read back as default.
- No installation, service restart, Core changes or model requests. Accepted runs retain their frozen settings. A pending explicit one-turn override still wins; choose Default in the composer or start a fresh session for the benchmark.

## Source changes, initially build-only

These source defaults and tooltips were subsequently installed with revision `a3a8367-dirty-d706d2b3a3ab`; the local installation receipt is retained privately.

- Fresh configuration explicitly selects default; empty/unknown preset fallback and no-client reporting also select default. API validation continues rejecting unknown preset names.
- Frontend initial settings state agrees with the backend default. Tooltips describe Core configuration without assuming one model's numbers or promising Strict quality.
- Existing saved explicit choices are not silently migrated. The operator's current installation was changed through its normal persisted settings endpoint.
- Test coverage: fresh-client wire omission, fallback behavior, configured preset values, and persistence/reload of Default. Existing tests continue covering explicit overrides and active-run snapshot isolation.

## Validation

- New regression assertions failed before implementation and passed afterward.
- go test -race -timeout 90s ./...: first run failed TestCheckPageEvalReturnsValues after a browser timeout with no DOM/result. An isolated rerun passed, then the full suite passed. No browser-tool code was changed; the transient failure's root cause remains undiagnosed.
- go vet ./...: passed.
- panel npx vitest run: 71 tests passed.
- panel npx svelte-check --tsconfig ./tsconfig.json: zero errors, 16 existing warnings.
- dgv/loop-contract: zero lint errors or warnings; policy node remains done and distinguishes live settings from uninstalled source changes.

## Benchmark

See [the replacement voxel prompt](benchmark-voxel-LABRIG.md). It retains the demanding shared-state implementation but adds deterministic fixtures, precise tick durations and input access, correct source/enclosure lighting expectations, AO compatibility rules, separate browser evidence, and explicit FAIL/UNVERIFIED reporting. It allows DGV and evidence directories. No voxel benchmark was run during this change.
