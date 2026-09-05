# Baseline contract probes

These are review artifacts, not product implementation. They assert desired behavior and intentionally fail against commit `510df03` (2026-09-05). Keep their assertions intact when turning the design into regression tests. Some probes use existing package test fixtures `newScriptedModel` and `gateFixture`; the loop audit fixture itself shares one event writer per temporary session.

From the Cerveau repo root:

```sh
go test -overlay docs/loop-audit-probes/overlay.json -vet=off -count=1 -run '^TestAudit' -v ./internal/loop ./internal/window ./internal/tools
```

Go overlay injects test-only files without editing the runtime packages. Paths in the overlay are relative to the current directory. Expected: exit 1, 16 failing leaf checks (the controls test has three subtests). `-vet=off` keeps the dedicated reproduction separate from the existing unreachable-code vet failure.

From `panel/`, with its existing node_modules installed:

```sh
./node_modules/.bin/vitest run --config ../docs/loop-audit-probes/vitest.config.mjs
```

Expected: exit 1, six failing tests. This config does not change the normal panel test selection. It compiles actual API/store code but mocks browser storage, network and audio; no real API/model requests occur. The external-run polling test uses a captured timer callback, not real delays. The null-response API probe demonstrates loss of the error; the eventual API may satisfy the contract by throwing a typed exception or returning a discriminated error result, so adapt that probe to the agreed transport shape.

These cases cover specific invariants, not every issue in the audit. An expected failure must come from the named assertion, not compilation/import/environment errors. All 22 recorded checks reached their intended assertions on the baseline.
