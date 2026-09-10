# DGV — operator's card

## Invocation patterns
- `dgv-list {}` → `dgv-context {"name":"architecture","focus":"loop"}`.
- `dgv-read {"name":"architecture","on":"loop"}` for exact details.
- `dgv-check {"name":"architecture","checks":"all"}` before trusting paths.
- `dgv-catalog {}` before create; update needs the latest `graph_sha256`.

## Failure → fix
- `engine_unavailable` → operator installs the original engine beside the helper; do not install through a talent.
- `stale_graph` → read again and reconcile the actual intervening changes.
- `output_limit` → narrower focus or smaller limit; use read for one element.
- `invalid_patch` → explicit IDs, no remove/history; use the catalog's kinds.
- `inventory_limit` → select a smaller session workspace.

## Never
- Never treat unchanged hashes, existing files or passed lint as proof of implementation correctness.
- Never use DGV status changes to execute a Cerveau plan or claim its steps passed.
- Never replace a graph, remove nodes or suppress diagnostics through this pack.

## Idioms
- Graphs: session workspace `dgv/*.dgv.json`; names are not paths.
- Context is bounded. `unbased` means first observation; `changed` or `incomplete` means inspect sources. `unchanged` compares one supplied observation only.
- `graph_sha256` protects updates; `freshness.fingerprint` compares a source slice. They are different.
