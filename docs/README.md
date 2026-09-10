# Cerveau documentation

Start with the [Linux setup guide](GETTING_STARTED.md). Cerveau is an alpha;
dated records describe the code and checks at that time, not a promise about
every model, machine or workload.

## Use and extend

| Guide | What it covers |
| --- | --- |
| [Getting started](GETTING_STARTED.md) | Source build, local model endpoint, configuration and optional dependencies. |
| [Native debugging tools](native-debug-tools-2026-09-07.md) | Browser actions, runtime profiles, checks, diagnostics and their boundaries. |
| [Memory conventions](memory-embedding-conventions.md) | Embedding compatibility, retrieval and degraded behavior. |
| [Native RFX](rfx-native-0.6.md) | Packs, packaging and external prerequisites. |
| [GitHub RFX](rfx-github.md) | Repository operations and capability boundaries. |
| [DevCheck RFX](rfx-devcheck.md) | Browser evidence tooling. |
| [Core profiles](../deploy/profiles/README.md) | Advanced, hardware-specific deployment; not a universal installer. |
| [Security policy](../SECURITY.md) | Local trust, remote access and shell privileges. |

## Execution and recovery

- [Execution contract](loop-contract-2026-09-05.md): implementation boundaries
  and longer-term design. Proposed behavior is not necessarily shipped behavior.
- [Targeted file recovery](targeted-file-recovery-2026-09-06.md).
- [Recovery evidence and memory](recovery-memory-2026-09-07.md).
- [Legacy plan recovery](legacy-plan-recovery-2026-09-07.md).
- [Implementation follow-ups](backlog-fixes-2026-09-07.md): bounded adaptation,
  delivery ordering, tools and UI changes with their recorded checks.

## Evaluation

[Voxel-world benchmark prompt](benchmark-voxel-LABRIG.md) is a reproducible task
for a **new session in an empty disposable workspace**. Never run it in the
Cerveau source tree. Its required application checks are separate from
Cerveau's harness tests; the full benchmark is not claimed to pass.

The [LABRIG validation record](release-0.6-LABRIG.md) distinguishes unit tests,
scripted browser cases and real-model smoke tests. Missing or skipped browser,
model and hardware checks remain unverified outside their recorded scope.

## Release history

| Release | Focus |
| --- | --- |
| **0.6.0-alpha · LABRIG** · September 2026 | Stepwise execution, ownership, replay, recovery, native tools and current UI work. [Release record](release-0.6-LABRIG.md). |
| **0.5 · Cores** · August 2026 | Local inference profiles and hardware-oriented controls. |
| **0.4 · Pocket** · August 2026 | Paired remote access and the Android client. |
| **0.3 · Guidebook** · August 2026 | Documentation and RFX discovery. |
| **0.2.1 · RFX_UI** · August 2026 | RFX panel integration. |
| **0.2 · RFX** · August 2026 | Extensible reflex packs. |

Older measurements retain their original hardware, model and configuration
context. They are historical observations, not comparative performance claims.
For the earlier README and implementation history, use the repository's Git
history. Local-only handovers, standalone installation receipts, raw session
logs and generated build artifacts are kept outside the published tree.
