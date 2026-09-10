# Idle screen reconciliation — 2026-09-06

## Cause and change

The frontend kept the floating idle screen visible for `parked` and `waking`, not just the countdown warning. The server tracker also had no production caller reconciling its parked flag with the actual Core service, so refreshing could return an expired warning again.

- Observe the registered Core's user-systemd service with read-only `systemctl show`; do not contact the model endpoint to establish whether it is parked.
- Reconcile before idle status, health responses and the idle timer tick. Confirmed parked/waking/unavailable states suppress additional parking requests.
- Show the floating warning only during `warning`. Once parked, replace it with the nonblocking notice: “Core is already idle. Send a message to wake it.” Waking also uses a nonblocking notice.
- An expired countdown says “Waiting for the Core to go idle”; elapsed time alone is not proof of shutdown.
- A failed service is unavailable, not successfully idle. Unknown observations preserve existing state. Observation depends on a registered user-systemd Core unit; unmanaged endpoints are not confirmed by this mechanism.
- Health polling skips model HTTP probes while parked, waking or unavailable, avoiding socket-activation wakes from those probes.

Existing styling was preserved for this focused state fix. The UI guidance scan found an existing width-transition warning in App.svelte; that unrelated animation was not changed.

## Verification

- `go test ./internal/idle ./internal/api ./cmd/crv` — PASS.
- `go test -race ./internal/idle ./internal/api ./cmd/crv` — PASS.
- `node scripts/build-release.mjs` — PASS: 78 Vitest tests, full Go race suite, go vet, svelte-check (existing warnings; no errors), production assets and binary checksums.
- `CERVEAU_PLAYWRIGHT_MODULE=/home/shiny/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright/index.mjs node scripts/qa-recovery-ui.mjs` — PASS on desktop and mobile: warning closes after observed park, reload stays nonmodal, waking stays nonmodal, no page errors or horizontal overflow.
- Browser receipts and inspected screenshots: `build/recovery-ui-U0anip/results.json`, `desktop-idle-notice.png`, `mobile-idle-notice.png` in that directory. These use the real UI with mocked API responses, not a live Core lifecycle test.

Build: `build/cerveau-0.6.0-alpha-LABRIG-bmGXdc`, revision `a3a8367-dirty-8fdba1cf204e`. This receipt and final DGV status were recorded after the build.

Not installed. No service restart, Core lifecycle operation, benchmark workspace edit or original session modification was performed for this fix. Production behavior after installation remains unverified.

## Installation follow-up

Installed on 2026-09-06 at the user's explicit request after confirming `/api/sessions` had no running sessions. The paragraph above records the earlier build-only handoff.

- Live localhost:7700 revision: `a3a8367-dirty-8fdba1cf204e`.
- Verified backup and installation manifest: `/home/shiny/.crv/install-backup-harness-recovery-HuLS5D/install.json`. Previous binaries remain recoverable in `old/` and `retired/`; only crv and crvcli were replaced.
- Only cerveau.service was stopped/started (new PID 2141227). The installer initially refused replacement because the existing park watchdog stopped Core PID 2137418 at 06:46:22 CEST and systemd started PID 2139642 at 06:46:23. After inspecting the journal and unchanged protected files, the manifest retained the original observation and recorded the new activating PID. That same PID completed startup. No Core lifecycle commands or configuration changes were issued by this installation.
- Installation verification passed: binary bytes, live revision, sampling/thinking settings, protected config/service/pack/helper files and original Minecraft file/session journal unchanged.
- Live health: model, embedder and Typesense all healthy. Harness, embedder and selected Core services active/running. RFX errors empty. Served HTML, JavaScript and CSS match packaged assets.
- `/api/idle` reports active with a fresh countdown. The actual next park/reload cycle has not been forced; its UI behavior was verified with browser fixtures as recorded above.
