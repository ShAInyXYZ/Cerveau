# Handsoff — Cerveau RFX system

*For the agentic harness picking this up. Read this first; it is the full
working context as of 2026-08-01. Project: **Cerveau** — local-first agentic
coding harness (Go core + Svelte 5 panel + llama.cpp serving), v0.2, repo
`ShAInyXYZ/Cerveau`, branch `main`.*

---

## 1. What RFX is (the thing being built)

RFX is Cerveau's declarative capability format — the replacement for
skills.md/MCP, designed for small local models (32K context, consumer
hardware). A **reflex** is a `.rfx.yaml` file: typed params → GBNF grammar,
steps re-dispatched through the guarded registry, capability card enforced
in Go, fuzz contract at install. **RFX = the suite; talents = the reflexes
inside a pack.** Vocabulary matters to the user:

- **Reflex / talent** — one `.rfx.yaml` (a tool)
- **Pack** — a folder of talents with `pack.yaml` (e.g. `github/`)
- **RFX_UI** — the panel surface talents project into the chat

Specs (LOCAL ONLY — `docs-private/` is gitignored, see §7 open threads):
`docs-private/RFX.md` (frozen v1 spec + v1.1 addendum), `docs-private/RFX-AUTHORING.md`
(how to write reflexes), `docs-private/RFX-UI.md` (the card design format — READ
THIS before UI work).

## 2. Current state — everything works, all pushed

- **RFX v1.1 shipped**: loader (packs, two-pass validation, `.state.json`
  enable/disable), executor (typed+quoted substitution, mode fencing via
  `ModeTool`, depth cap), capability cards, fuzz contracts, Synapse exec
  tools, `crvcli rfx` (list/show/install/remove/enable/disable/test/distill)
- **Core tools added**: `glob` (safe), `apply_patch` (atomic multi-edit)
- **Live talent pack**: `~/.crv/rfx/github/` — git-status/diff/log/commit
  + `pack.yaml` v1.1.0 **with a `ui:` widget manifest** (status rows,
  buttons, field, log)
- **RFX_UI in the panel**: Settings → "RFX Talents" section (toggles via
  `/api/rfx` + `/api/rfx/toggle`), chat right-rail dock with pack cockpit
  card (`RfxPackCard.svelte`) + action bridge (`POST /api/rfx/run` →
  `loop.RunReflex` → guarded registry)
- Suite: `go test ./...` = 11/11 packages green. Keep it that way.

## 3. THE NEXT TASK (what the user wants now)

**Rebuild the dock as a Blender N-panel.** Current dock is a 292px-wide
card panel — user rejected it ("no need for that big docking panel").
Target: **a slim vertical icon strip docked at the chat's right edge —
one icon per pack; clicking an icon expands that pack's panel.** Strip is
always thin (~34px); the panel opens on demand.

- **Open question the user never answered**: expand **inline** (rail widens
  for the open pack) vs **overlay** (popover next to strip). Recommendation:
  inline-expand to ~292px showing ONLY the open pack's card — simplest,
  matches ZCode's own collapsible rail behavior, no z-index fights.
  Confirm with user if possible; if not, build inline.
- The collapsed strip in `RfxDock.svelte` is already 80% of the icon rail
  — generalize it: one icon button per pack (with `enabled.length` badge),
  plus one per standalone reflex group.
- Panel content = the existing `RfxPackCard.svelte` — it stays as-is
  (it's the approved cockpit: metrics, buttons, field binding, log tail).
- Keep: declarative-only rule (NO user JS/HTML in cards ever), the
  `ui:` manifest format in `docs-private/RFX-UI.md`, hardware-panel identity
  (mono `mk`/`mv` metrics, hairline inset rings, tokens from
  `panel/src/lib/SysMonitor.svelte`).

## 4. File map (what lives where)

```
internal/rfx/           the format: rfx.go (types+validate), loader.go
                        (packs+state), card.go (permissions), fuzz.go
                        (arg-gen+contract), *_test.go (41+ tests)
internal/tools/         reflextool.go (pipeline executor + AddReflexes),
                        execreflex.go (Synapse exec), fuzz.go (dry-run
                        harness), glob.go, applypatch.go (new core tools),
                        registry.go (Tool/ModeTool/WithReflexes/ReflexNames)
internal/api/rfx.go     GET /api/rfx, POST /api/rfx/toggle, POST /api/rfx/run
internal/loop/loop.go   SetReflexes, per-turn WithReflexes, prompt block
                        (RFX inventory), RunReflex (dock bridge)
cmd/crv/main.go         loader wiring (pointer-following predicate)
cmd/crvcli/rfx.go       all rfx subcommands (pack-aware)
panel/src/lib/          RfxDock.svelte (THE FILE TO REDESIGN, see §3),
                        RfxPackCard.svelte (approved cockpit, reuse),
                        Settings.svelte (RFX section), SysMonitor.svelte
                        (identity reference), App.svelte (chatwrap wiring)
docs-private/                   RFX.md, RFX-AUTHORING.md, RFX-UI.md (LOCAL ONLY)
docs-private/architecture/      Diagram.json + Build.json — arch/build boards,
                        view via docs-private/arch-viewer (npm run dev, port 5181)
~/.crv/rfx/             LIVE reflex dir (github/ pack lives here, NOT in repo)
rfx/                    repo's canonical pack dir (currently EMPTY —
                        user cleared it for ground-up redesign; promote
                        packs here only when the user approves)
```

## 5. How to work in this repo (house rules — user enforces these)

1. **Step by step, never big-bang.** The user assigns one step at a time;
   fix holes found at each step and DOCUMENT them (Build.json notes).
2. **Test everything, dogfood for real.** `go build ./... && go vet ./...`
   and `go test ./...` after every change. Live-verify with `crvcli rfx
   test <name>` (fuzz) and real panel screenshots via headless chromium:
   `~/.cache/ms-playwright/chromium_headless_shell-1228/chrome-headless-shell-linux64/chrome-headless-shell --headless --disable-gpu --no-sandbox --window-size=1400,800 --virtual-time-budget=8000 --screenshot=/tmp/x.png "http://localhost:5171/"`
3. **Dev environment**: core = `go run ./cmd/crv` (port 7700, restart
   after Go changes — kill by `pkill -f "exe/crv"` AND check
   `ss -tlnp | grep 7700`, zombie go-run children survive pkill);
   panel = `cd panel && npm run dev` (port 5171, hot-reload, use THIS
   URL); panel prod build = `cd panel && npm run build` (writes
   internal/panel/dist, then rebuild core binary).
4. **Commit per step**, conventional style (`feat(rfx-ui): …`), push to
   main (user's solo repo). NEVER commit `docs-private/`, `Reports/`, binaries
   (`.gitignore` anchors `/crv` `/crvcli` — bare patterns once hid the
   entire cmd/ source; check `git status --ignored` if files vanish).
5. **Panel gotchas**: Svelte 5 runes; never bind to `obj[key].field`
   where obj[key] may be undefined (use getter/setter helpers — this
   exact bug shipped once); after editing, `cd panel && npx svelte-check
   --threshold error && npm run build`, then screenshot-verify.
6. **Shell gotchas**: `grep -c` in an &&-chain exit-1s on zero matches
   and silently skips later commands (bit us twice — split commands).
7. **Registry is the single door**: all tool execution goes through
   `Registry.ExecuteMode` (guard → remediator → dispatch → ingress cap).
   Never bypass it. Declared risk/modes are enforced there.
8. **Process**: track milestone work in `docs-private/architecture/Build.json`
   (add nodes with kind todo/wip/done, notes recording decisions+holes).

## 6. Design invariants (do not violate without asking the user)

- **No prose in the context window.** Model sees name+schema via GBNF.
- **No user JavaScript/HTML in RFX_UI.** Cards are data; renderer owns pixels.
- **Restrictions are the feature.** Closed widget set, closed conditional
  language (`when: steps.ID.ok|failed` only), one folder level for packs.
- **Honest scoping.** Cards are "a safety floor, not a sandbox"; failures
  keep real stderr; every limitation is documented, not papered over.
- **Core vs RFX**: universal+jail-needing tools are CORE (Go); everything
  else is RFX. Git was deliberately made RFX (pack), glob/apply_patch core.

## 7. Open threads (after the N-panel rebuild, in rough priority)

1. **The bracket session** — never run: a live autopilot test of the
   agent USING reflexes end-to-end (the system's raison d'être).
2. **`docs:` recall-indexing** — operator's cards into Typesense
   (user's "LoRA-like docs" idea; v1.1 candidate recorded in spec §9).
3. **Spec files not in git** — `docs-private/RFX.md`, `RFX-AUTHORING.md`,
   `RFX-UI.md` are gitignored; user was warned, hasn't decided. Suggest
   `!docs-private/RFX*.md` exceptions in .gitignore.
4. **Episodic card refresh** — dock cards updating when the MODEL runs a
   reflex (edge exists in Diagram.json; not implemented; v2).
5. **Promote github pack to repo `rfx/`** — local-only now; user decides.
6. **Remaining widget types**: toggle, progress (defined in RFX-UI.md,
   unimplemented).

## 8. The user's taste (read before designing anything)

- Blender N-panel is the UI north star (slim, vertical, expandable).
- Hardware/system panels in the header are the visual identity
  (SysMonitor.svelte tokens: mono metrics, inset rings, segmented bars).
- Hates overflow/scroll traps, wasted space, fat chrome.
- Wants straight answers with confidence numbers, not hedging.
- French-branded, lowercase-mono aesthetic; accent #E88BA0 / #C0304A on
  ink #17140F, cream #F2E1DE.
