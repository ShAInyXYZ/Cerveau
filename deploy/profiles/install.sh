#!/usr/bin/env bash
# Install a Brain Core profile: its systemd units, its cores.json entry, and its
# place in the park watchdog's unit list. Idempotent — run it again after
# editing a profile and it updates in place.
#
#   deploy/profiles/install.sh deploy/profiles/<profile-dir>
#
# It never starts the Core and never touches the `active` Core. Choosing is done
# in the panel (Settings → Engine) or by editing ~/.config/cerveau/cores.json.
set -euo pipefail

PROFILE="${1:?usage: install.sh <profile-dir>}"
[[ -f "$PROFILE/core.json" ]] || { echo "no core.json in $PROFILE" >&2; exit 1; }

UNITS="$HOME/.config/systemd/user"
CORES="${CRV_CORES_JSON:-$HOME/.config/cerveau/cores.json}"
mkdir -p "$UNITS" "$(dirname "$CORES")"

# 1. Units. Only the .socket gets enabled: the Core unit is pulled up by the
#    proxy on the first request, and enabling it would load weights at boot.
shopt -s nullglob
for u in "$PROFILE"/*.service "$PROFILE"/*.socket; do
  install -m 0644 "$u" "$UNITS/"
  echo "unit    $(basename "$u")"
done
systemctl --user daemon-reload
for s in "$PROFILE"/*.socket; do
  systemctl --user enable --now "$(basename "$s")"
  echo "socket  $(basename "$s") enabled"
done

# 2. cores.json — replace the entry with the same id, or append. `active` is
#    left alone: installing a Core is not the same as choosing it.
#    The Core unit's Environment= lines (minus PORT) become the entry's
#    `params`: the profile's defaults, which the panel shows and can override
#    through ~/.config/cerveau/cores.d/<id>.env (the unit's EnvironmentFile).
CORE_UNIT=$(ls "$PROFILE"/*.service | grep -v -- '-proxy.service' | head -1)
python3 - "$PROFILE/core.json" "$CORES" "$CORE_UNIT" <<'PY'
import json, sys, os, re
new = json.load(open(sys.argv[1]))
path = sys.argv[2]
params = {}
for line in open(sys.argv[3]):
    m = re.match(r'^Environment=(?:"([A-Z][A-Z0-9_]*)=([^"]*)"|([A-Z][A-Z0-9_]*)=(\S*))\s*$', line)
    if m:
        k, v = (m.group(1), m.group(2)) if m.group(1) else (m.group(3), m.group(4))
        if k != "PORT": params[k] = v
if params: new["params"] = params
new.setdefault("unit", os.path.basename(sys.argv[3]))
# embed.env next to core.json: where the embedder runs under this profile.
# Applied by Restart (written to ~/.config/cerveau/embed.env, unit restarted).
embed_env = os.path.join(os.path.dirname(sys.argv[1]), "embed.env")
if os.path.exists(embed_env):
    emb = {}
    for line in open(embed_env):
        m = re.match(r'^([A-Z][A-Z0-9_]*)=(.*)$', line.strip())
        if m: emb[m.group(1)] = m.group(2).strip('"')
    if emb: new["embed"] = emb
envfile = f"cores.d/{new['id']}.env"
if not any(envfile in l for l in open(sys.argv[3])):
    print(f"warning: {os.path.basename(sys.argv[3])} has no EnvironmentFile for {envfile}; panel overrides will not apply", file=sys.stderr)
reg = json.load(open(path)) if os.path.exists(path) else {"active": "", "cores": []}
reg.setdefault("cores", [])
for i, c in enumerate(reg["cores"]):
    if c.get("id") == new["id"]:
        reg["cores"][i] = new; break
else:
    reg["cores"].append(new)
json.dump(reg, open(path, "w"), indent=2, ensure_ascii=False); open(path, "a").write("\n")
print(f"core    {new['id']} -> {path} ({len(reg['cores'])} cores) params={list(params)} embed={list(new.get('embed',{}))}")
PY

# 3. Park watchdog: it stops every installed crv-core-*.service (never a
#    .socket — that is what wakes the Core again). Rebuilt from what is on disk.
DROP="$UNITS/crv-park-watchdog.service.d"
mkdir -p "$DROP"
list=$(cd "$UNITS" && ls crv-core-*.service | tr '\n' ' ')
printf '[Service]\nEnvironment="CRV_CORE_UNITS=%s"\n' "${list% }" > "$DROP/profiles.conf"
systemctl --user daemon-reload
systemctl --user try-restart crv-park-watchdog.service 2>/dev/null || true
echo "park    CRV_CORE_UNITS=${list% }"
