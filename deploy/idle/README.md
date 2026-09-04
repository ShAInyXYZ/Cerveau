# Idle parking

Cerveau parks the Brain Core after a period of true silence, and wakes it on the
next real request. The point is not to save a few watts during a working day —
it is that a Core left loaded overnight spends the night thinking about nothing.

## The split

Two halves, deliberately separated:

| Half | Owns | Lives in |
|---|---|---|
| **Cerveau** | deciding *when* — idle timer, holds, the warning screen | `internal/idle` |
| **systemd** | actually stopping and starting the Core | this directory |

`internal/cores` states the invariant this preserves: *a harness that can stop
the engine it is talking to has a failure mode where the machine ends up with no
model at all, and no way to say so.* So Cerveau never runs `systemctl`. It
writes a request file; the watchdog acts on it. If Cerveau crashes mid-park,
systemd still holds both ends.

## Flow

```
 silence ──► Cerveau warns (15 min left) ──► user answers or does not
                                              │
                            ┌─────────────────┴──────────────────┐
                       "stay awake"                        no answer
                            │                                   │
                     timer pushed out                  park-request.json
                                                               │
                                              watchdog stops the core unit
                                                               │
                                     next request ──► socket activation ──► core loads
```

## What holds the timer

The timer counts **silence, not wall-clock**. Anything in flight holds it:
an in-progress generation, a running loop, a queued task. A workflow left to run
unattended will not be parked out from under itself — that was an explicit
requirement, and `TestHoldStopsTheClock` covers it.

## Install

```bash
cp crv-park-watchdog.sh ~/.local/bin/
cp crv-park-watchdog.service crv-core-vllm.service \
   crv-core-vllm-proxy.service crv-core-vllm-proxy.socket \
   cerveau-embed.service \
   ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now crv-park-watchdog.service
systemctl --user enable --now crv-core-vllm-proxy.socket
```

Do **not** enable `crv-core-vllm.service` itself — it is pulled up by the
proxy on demand. Enabling it would load the Core at boot, which is the exact
thing this feature exists to avoid.

Set `CRV_CORE_UNITS` to the units that hold your weights:

```bash
systemctl --user edit crv-park-watchdog.service
# [Service]
# Environment=CRV_CORE_UNITS=vllm.service llama-server.service
```

## Waking

Parking is only safe if waking is automatic. Three units do it:

| Unit | Role |
|---|---|
| `crv-core-vllm-proxy.socket` | holds **:18020** — the endpoint in `cores.json` |
| `crv-core-vllm-proxy.service` | `systemd-socket-proxyd` → forwards to :18021 |
| `crv-core-vllm.service` | vLLM itself, listening on **:18021** |

Cerveau always talks to :18020. Whether the Core behind it is loaded is
systemd's problem, not the harness's.

### Three things that are easy to get wrong

**The socket must name-match the PROXY, not the Core.** systemd activates the
unit sharing the socket's base name. Point it at vLLM and vLLM inherits the
socket FD — which it ignores, because it binds its own port. Traffic then
never arrives. Hence `crv-core-vllm-proxy.socket` ↔
`crv-core-vllm-proxy.service`.

**The proxy is `Type=simple`, not `notify`.** `systemd-socket-proxyd` never
calls sd_notify. Under `Type=notify` systemd killed the proxy at the 90 s
start timeout and the socket respawned it, so every model call in flight at
that moment died with EOF — "model call failed — retrying", every ~92 s
(found 2026-09-04). Readiness of the Core is the Core unit's job, below.

**`Type=exec` means "process spawned", not "port open".** vLLM needs 60–120 s
before it binds. Without a readiness gate the proxy forwards into a closed
port and the first request after a park fails. `ExecStartPost` polls :18021
until it answers.

**That poll must watch `$MAINPID`.** If vLLM crashes, a bare port-poll keeps
looping for the full `TimeoutStartSec` — the unit sits in `activating`, and
every client blocks on the socket the whole time. Observed in testing: an
8-minute hang after a crash that took 17 seconds. `kill -0 $MAINPID` makes it
fail fast.

### Verified behaviour

Round trip, measured:

```
parked  ──request──►  HTTP 200 in 2.0s   (client WAITS, does not fail)
warm    ──request──►  HTTP 200 in 0.001s
park request written ──► core stopped in ~15s, request file consumed
```

## Switching Cores (the panel's Restart button)

Same split. Settings → Engine → Profile → **Restart** makes the API write
`switch-request.json` next to the park request; the watchdog picks it up:

```
stop every other Core unit  ──►  systemctl --user start <chosen unit>  ──►  restart cerveau.service
(two vLLM Cores share GPU 0)      (blocks until the port answers)            (LLM client re-reads the endpoint)
```

The embedder follows the profile: the API writes `~/.config/cerveau/embed.env`
from the Core's `embed` settings (`cerveau-embed.service` reads it through
`EnvironmentFile=`; unit source in this directory) and the watchdog restarts
it before Cerveau. Progress goes to `switch-status.json` (`queued → stopping → starting →
restarting → done | failed`), which `/api/cores` exposes as `switch` and the
panel polls. A Core needs a `unit` in `cores.json` to be switched this way;
`deploy/profiles/install.sh` sets it, the two original Cores were given theirs
by hand. Without one, the panel shows the commands to run instead.

This is the one place anything but a socket starts a Core — and it is still
systemd doing it, on a request file, never the harness.

## Cold-start cost

| Core | Warm reload | Why |
|---|---|---|
| llama.cpp · MoE | ~10–25 s | GGUF stays in page cache |
| vLLM · Dense | ~60–120 s | KV pool + CUDA graph capture rebuilt |

What is lost on a park is the **active cache** — the KV/prefix cache of the
current operation. Sessions, history and long-term memory are on disk and come
back untouched. The Idle screen says so explicitly, because "cache lost" reads
as "conversation lost" to anyone who has not read this file.

## Known constraint: vLLM cannot start while ComfyUI holds the GPU

Verified on this machine, 2026-08-25:

```
ValueError: Free memory on device cuda:0 (1.63/23.56 GiB) on startup is less
than desired GPU memory utilization (0.9, 21.2 GiB)
```

`GPU_UTIL=0.90` wants 21.2 GB of a 24 GB card. ComfyUI (port 8188) holds
~22 GB, so vLLM cannot claim its pool and the wake fails — the socket answers,
the Core dies, the client gets nothing.

This is not an idle-parking bug; it is the same coexistence limit that already
governs this rig. But it interacts badly with parking: a parked Core that
cannot restart is worse than one that never parked. If both are to run, either
drop `GPU_UTIL` or stop ComfyUI before the Core wakes.

The full park→wake cycle was proven with a stand-in backend on :18021, so the
unit topology is correct and independent of this.
