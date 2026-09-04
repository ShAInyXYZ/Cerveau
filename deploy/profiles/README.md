# Core profiles

A **profile** is one Brain Core made installable: the systemd units that run
it and park it, plus the `cores.json` entry that lets the panel choose it.
Profiles are added next to each other — installing one never changes the
Cores you already have.

```
deploy/profiles/
  install.sh                     install or update one profile
  <profile>/
    core.json                    the entry for ~/.config/cerveau/cores.json
    crv-core-<id>.service        the engine itself, on a BACKEND port
    crv-core-<id>-proxy.service  systemd-socket-proxyd → backend port
    crv-core-<id>-proxy.socket   the PUBLIC port — what core.json points at
    embed.env                    optional: where the embedder runs under this Core
```

Every Core gets its own pair of ports, so several can be installed at once.
Only one heavy Core can hold the GPUs at a time; the park watchdog stops
whichever is loaded when Cerveau asks, and the socket wakes the chosen one on
the next request. Choosing is done in **Settings → Engine** or by editing
`active` in `~/.config/cerveau/cores.json`, then restarting Cerveau.

| Profile | Core id | Public → backend | What it is |
|---|---|---|---|
| (deploy/idle) | `vllm` | 18020 → 18021 | Qwen3.8-27B W4A16, one 3090 |
| `qwen3.8-27b-bf16-tp4` | `vllm-27b-bf16` | 18030 → 18031 | Qwen3.8-27B BF16 weights + bf16 KV + vision + prefix cache, TP=4 on 4×3090, 262k · embedder on the 3060 |
| `qwen3.8-27b-w8a16-tp4` | `vllm-27b-w8a16` | 18040 → 18041 | Qwen3.8-27B W8A16 + MTP, fp8 KV on FlashInfer, TP=4 on 4×3090, 262k · the marlin int8 patches finally engage |

Each TP=4 profile has its own diagram in `dgv/` — what wakes it, what it
loads, how the cards split it, and what its config overrides. They are
deliberately standalone, so reading one Core does not mean reading the whole
system: `profile-w8a16-tp4`, `profile-bf16-tp4`, and `Architecture` for how a
Core fits into Cerveau. Open with `dgv_open` or the viewer on :7710.

Both TP=4 profiles run a **patched** vLLM. One of those patches gates a
setting: `PREFIX_CACHE=1` is only safe on Qwen3.8 with
`vllm-pr48375-mamba-prefix-eagle.patch` applied, because without it a cached
page can carry recurrent state built over rejected draft tokens — silently
wrong output, never an error. See **[ENGINE-PATCHES.md](ENGINE-PATCHES.md)**
for the full list, how to check whether they are applied, and what to do after
a vLLM upgrade.

## Install

```bash
deploy/profiles/install.sh deploy/profiles/qwen3.8-27b-bf16-tp4
```

Copies the units, enables the socket only, merges `core.json` into
`cores.json`, and rebuilds the watchdog's `CRV_CORE_UNITS` from every
`crv-core-*.service` on disk. Re-run after editing a profile.

## Add a new profile

Both TP=4 profiles share one launcher, `Gpu-Rig/quad/start_qwen_tp4.sh`; what
differs between them is entirely in their unit's `Environment=` lines (`MODEL`,
`DTYPE`, `KV`, `MARLIN_INT8`), which is also what the panel shows and edits.

1. Copy an existing profile directory. Pick an `id` (letters, digits, dashes)
   and a free port pair — next free is 18050/18051.
2. Rename the three units to `crv-core-<id>…`. The `.socket` and the
   `-proxy.service` **must share a base name**; systemd activates the unit
   named like the socket, and that has to be the proxy, not the engine.
3. In the Core unit: `ExecStart` is the launcher, `PORT` is the backend port,
   and the `ExecStartPost` poll must check that same port.
4. In the proxy service: the address after `systemd-socket-proxyd` is the
   backend port. In the socket: `ListenStream` is the public port.
5. `core.json`: `endpoint` is the public port; `start` is the command a
   human runs to pre-warm it (the socket does it automatically otherwise);
   `notes` is what the tooltip shows — say what the trade is.
6. `install.sh <dir>`, then pick it in the panel.

## Parameters from the panel

Choosing a Core switches everything about it — each profile is its own unit,
with its own KV dtype, vision, window and GPU pool. `install.sh` copies the
Core unit's `Environment=` lines into the entry as `params`, so **Settings →
Engine → Profile** shows what the chosen Core runs with and lets you override
any of it. Overrides are written to `~/.config/cerveau/cores.d/<id>.env`, which
the unit reads through `EnvironmentFile=` at its next start — a park/wake
cycle or a restart. Only the diff is stored; reset removes the file. `PORT`
cannot be overridden, the socket proxy forwards to it.

A profile that wants this must carry the line
`EnvironmentFile=-%h/.config/cerveau/cores.d/<id>.env` after its
`Environment=` block; `install.sh` warns when it is missing.

## The embedder follows the profile

A profile may carry an `embed.env` next to `core.json`: the environment for
`cerveau-embed.service` (`EMBED_DEVICE`, `CUDA_VISIBLE_DEVICES`,
`EMBED_THREADS`). `install.sh` copies it into the entry as `embed`; the panel
shows it as the **Embedder** group and lets you override it. **Restart** writes
the active profile's effective settings to `~/.config/cerveau/embed.env`
(the unit's `EnvironmentFile=`) and the watchdog restarts the embedder. A
profile without `embed.env` clears the file: the unit's own default, CPU,
which is right for a one-GPU machine. The BF16 profile puts it on the 3060.

## Switching from the panel

Settings → Engine shows one card per engine and, under it, that engine's
profiles. **Restart** asks the park watchdog to park the other Cores, start
the chosen one and restart Cerveau (see `deploy/idle/README.md`); it needs the
entry's `unit`, which `install.sh` fills in from the profile's Core unit.

The panel never starts an engine. `start` is shown and copied, not executed —
a harness that can stop the Core it is talking to can leave the machine with
no model at all.
