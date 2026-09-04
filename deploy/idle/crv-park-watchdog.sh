#!/usr/bin/env bash
# Park the Brain Core when Cerveau asks.
#
# Cerveau decides WHEN; this script is the only thing that stops a unit. That
# split is the whole safety property: the harness cannot stop the engine it is
# talking to, so it cannot strand the machine with no model. If Cerveau dies
# mid-transition, systemd still owns both ends.
#
# Install:
#   cp crv-park-watchdog.sh ~/.local/bin/
#   systemctl --user enable --now crv-park-watchdog.service
set -euo pipefail

RUN="${XDG_RUNTIME_DIR:-$HOME/.crv/run}/cerveau"
REQ="$RUN/park-request.json"
SWITCH="$RUN/switch-request.json"
SWITCH_STATUS="$RUN/switch-status.json"
POLL="${CRV_PARK_POLL:-15}"

# The units that hold the weights. Adjust to match your cores.json.
#
# Only the CORE units are listed — never the .socket. Stopping the socket would
# remove the very thing that wakes the Core back up, turning a park into an
# outage that needs a manual start.
CORE_UNITS="${CRV_CORE_UNITS:-crv-core-vllm.service crv-core-vllm-proxy.service}"

park() {
  local stopped=0
  for u in $CORE_UNITS; do
    if systemctl --user is-active --quiet "$u" 2>/dev/null; then
      logger -t crv-park "stopping $u"
      systemctl --user stop "$u" && stopped=1
    elif systemctl is-active --quiet "$u" 2>/dev/null; then
      logger -t crv-park "stopping (system) $u"
      sudo -n systemctl stop "$u" && stopped=1
    fi
  done
  return $(( stopped ? 0 : 1 ))
}

# Switching: the panel's Restart button. The API wrote which Core to bring up
# and which units to stop first (every other Core — two vLLM Cores share GPU 0,
# and the second one started into a full card OOMs). Then Cerveau restarts so
# its LLM client and window pick up the new endpoint. Progress goes to a
# status file the panel polls. This is the ONE place a Core is started by
# anything but its socket — and it is still systemd doing it, not the harness.
status() {  # phase detail
  printf '{"core":"%s","phase":"%s","detail":"%s","updated_at":"%s"}\n' \
    "$1" "$2" "$3" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$SWITCH_STATUS"
}
switch_core() {
  local core unit restart stop
  core=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["core"])' "$SWITCH")
  unit=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["unit"])' "$SWITCH")
  restart=$(python3 -c 'import json,sys;print("1" if json.load(open(sys.argv[1])).get("restart_cerveau") else "0")' "$SWITCH")
  restart_embed=$(python3 -c 'import json,sys;print("1" if json.load(open(sys.argv[1])).get("restart_embed") else "0")' "$SWITCH")
  stop=$(python3 -c 'import json,sys;print(" ".join(json.load(open(sys.argv[1])).get("stop",[])))' "$SWITCH")
  rm -f "$SWITCH"
  logger -t crv-park "switch → $core ($unit)"
  status "$core" stopping "parking every other Core"
  # The other Cores' wake SOCKETS go down too. A parked Core whose socket still
  # listens is one stray probe away from loading itself into GPUs the chosen
  # Core now owns — seen 2026-09-04, seconds after a switch. Only the active
  # Core is wake-able; that is what "active" means. Parking (below) never
  # touches sockets, so the active Core still wakes on demand.
  for u in $stop $CORE_UNITS; do
    [[ "$u" == "$unit" || "$u" == "${unit%.service}-proxy.service" ]] && continue
    [[ "$u" == *-proxy.service ]] && continue   # handled with its Core below
    for v in "${u%.service}-proxy.socket" "${u%.service}-proxy.service" "$u"; do
      # NOT `is-active --quiet`: a Core that is still LOADING reports
      # "activating", which that test treats as stopped. The W8A16 switch then
      # started into a GPU the old Core was still filling and died with
      # "Free memory on device cuda:0 (1.91/23.56 GiB)" (2026-09-04).
      st=$(systemctl --user is-active "$v" 2>/dev/null || true)
      case "$st" in
        active|activating|reloading|deactivating)
          logger -t crv-park "switch: stopping $v ($st)"; systemctl --user stop "$v" || true ;;
      esac
    done
  done
  if systemctl --user cat "${unit%.service}-proxy.socket" >/dev/null 2>&1; then
    systemctl --user start "${unit%.service}-proxy.socket" || true
  fi
  # systemctl stop returns when the process is gone, but CUDA can take a few
  # seconds more to hand the memory back. Starting into that window is the
  # same OOM by another route, so wait for the cards to actually clear.
  for _ in $(seq 1 30); do
    busy=$(nvidia-smi --query-compute-apps=used_memory --format=csv,noheader,nounits 2>/dev/null | awk '$1>2000' | wc -l)
    [ "${busy:-0}" -eq 0 ] && break
    status "$core" stopping "waiting for the previous Core to release its VRAM"
    sleep 2
  done
  status "$core" starting "loading $unit — a cold start can take minutes"
  # restart, not start: Restart on the Core that is already answering means
  # "reload it with the saved parameters", and `start` on an active unit is a
  # no-op — the panel said done in three seconds and nothing had changed.
  if ! systemctl --user restart "$unit"; then
    logger -t crv-park "switch: $unit failed to start"
    status "$core" failed "$unit did not come up — journalctl --user -u $unit"
    return 1
  fi
  if [[ "$restart_embed" == "1" ]]; then
    # The embedder follows the profile (~/.config/cerveau/embed.env was just
    # rewritten by the API): CPU on a one-GPU box, the 3060 on the rig.
    status "$core" restarting "restarting the embedder with the profile's placement"
    systemctl --user restart cerveau-embed.service || logger -t crv-park "switch: embedder restart failed"
  fi
  if [[ "$restart" == "1" ]]; then
    status "$core" restarting "restarting cerveau.service so the harness picks up the endpoint"
    systemctl --user restart cerveau.service || true
  fi
  status "$core" done "$unit is answering"
  logger -t crv-park "switch → $core done"
}

while true; do
  if [[ -f "$SWITCH" ]]; then
    switch_core || true
  fi
  if [[ -f "$REQ" ]]; then
    # Consume the request FIRST. A request that survives its own handling
    # would re-park the Core immediately after the user wakes it.
    rm -f "$REQ"
    if park; then
      logger -t crv-park "core parked"
    else
      logger -t crv-park "park requested but no core unit was running"
    fi
  fi
  sleep "$POLL"
done
