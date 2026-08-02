<script>
  import { Play, Zap } from 'lucide-svelte';
  import { rfxIcon } from './rfxIcons.js';
  import { j, jpost } from './api.js';
  import RfxPackCard from './RfxPackCard.svelte';
  import RfxCustomPanel from './RfxCustomPanel.svelte';

  // RfxDock — Blender-N-panel-style RFX surface (docs/RFX-UI.md).
  // A slim vertical tab strip lives at the chat's right edge — one rotated
  // tab per pack (plus one for standalone reflexes). Clicking a tab expands
  // that pack's panel INLINE next to the strip; clicking it again collapses
  // back to the bare strip. Only one pack is open at a time.
  let { sessionId = null, onTurn = null } = $props();
  let data = $state({ packs: [], reflexes: [] });
  let runs = $state({});
  let args = $state({});
  let armed = $state('');            // name of the dangerous reflex awaiting confirm
  // ?rfx=<pack> deep-links a panel open (also how CI screenshots verify it)
  let openTab = $state(new URLSearchParams(location.search).get('rfx')
    ?? localStorage.getItem('rfxdock-tab') ?? '');

  async function load() {
    try { data = await j('/api/rfx'); } catch { /* older core */ }
  }
  load();

  const enabled = $derived((data.reflexes ?? []).filter((r) => r.enabled));
  const uiPacks = $derived((data.packs ?? []).filter((p) => (p.ui?.widgets ?? []).length > 0 || p.has_panel));
  const membersOf = $derived((name) => enabled.filter((r) => r.pack === name));
  // the card gets ALL members — it greys buttons whose target is disabled
  const allMembersOf = $derived((name) => (data.reflexes ?? []).filter((r) => r.pack === name));
  // standalone group: reflexes with no pack, or whose pack declares no ui
  const defaults = $derived(enabled.filter((r) => !r.pack || !uiPacks.some((p) => p.name === r.pack)));

  // tabs: packs with a live panel + one "rfx" tab for the standalone group
  const tabs = $derived([
    // a UI-only pack (panel, zero talents) is a supervisor — it still gets a tab
    ...uiPacks.filter((p) => membersOf(p.name).length > 0 || p.ui_only)
      .map((p) => ({ id: p.name, count: membersOf(p.name).length, icon: p.icon })),
    ...(defaults.length ? [{ id: '·rfx', count: defaults.length, icon: 'zap' }] : [])
  ]);
  const openPack = $derived(uiPacks.find((p) => p.name === openTab));
  const openValid = $derived(tabs.some((t) => t.id === openTab));

  function pick(id) {
    openTab = openTab === id ? '' : id;
    localStorage.setItem('rfxdock-tab', openTab);
  }

  function getArg(name, p) { return args[name]?.[p] ?? ''; }
  function setArg(name, p, v) { (args[name] ??= {})[p] = v; }

  function paramsOf(r) {
    const props = r.params?.properties ?? {};
    const required = new Set(r.params?.required ?? []);
    return Object.entries(props).map(([name, s]) => ({ name, required: required.has(name), ...(s ?? {}) }));
  }

  // Dangerous tier gets a two-click arm/confirm — a human misclick should
  // not fire a dangerous reflex, and neither should a stray local request
  // decide it's "just a button".
  async function run(r) {
    if (r.risk === 'dangerous' && armed !== r.name) {
      armed = r.name;
      setTimeout(() => { if (armed === r.name) armed = ''; }, 3000);
      return;
    }
    const confirmed = r.risk === 'dangerous';
    armed = '';
    const a = {};
    for (const p of paramsOf(r)) {
      const v = args[r.name]?.[p.name];
      if (v !== undefined && v !== '') a[p.name] = v;
    }
    runs[r.name] = { state: 'run', output: '' };
    try {
      const res = await jpost('/api/rfx/run', { name: r.name, args: a, confirmed });
      runs[r.name] = res.ok ? { state: 'ok', output: res.output || '(no output)' }
                            : { state: 'err', output: (res.output ? res.output + '\n' : '') + res.error };
    } catch (e) {
      runs[r.name] = { state: 'err', output: String(e) };
    }
  }
</script>

{#if tabs.length > 0}
  <div class="npanel">
    {#if openValid && openTab}
      <aside class="panel anim-rise">
        <div class="panel-body">
          {#if openPack?.has_panel}
            <RfxCustomPanel pack={openPack} members={allMembersOf(openPack.name)} {sessionId} {onTurn} />
          {:else if openPack}
            <RfxPackCard pack={openPack} members={allMembersOf(openPack.name)} />
          {:else}
            {#each defaults as r (r.name)}
              <div class="card">
                <div class="c-head">
                  <span class="c-name">{r.name}</span>
                  <span class="chip" class:chip-dangerous={r.risk === 'dangerous'} class:chip-sensitive={r.risk === 'sensitive'}>{r.risk}</span>
                </div>
                <div class="c-desc">{r.description}</div>

                {#each paramsOf(r) as p (p.name)}
                  <label class="field">
                    <span class="fname">{p.name}{p.required ? ' *' : ''}</span>
                    {#if p.enum}
                      <select value={getArg(r.name, p.name)} onchange={(e) => setArg(r.name, p.name, e.target.value)}>
                        <option value="">—</option>
                        {#each p.enum as e}<option value={e}>{e}</option>{/each}
                      </select>
                    {:else if p.type === 'boolean'}
                      <input type="checkbox" checked={!!getArg(r.name, p.name)} onchange={(e) => setArg(r.name, p.name, e.target.checked)} />
                    {:else if p.type === 'integer' || p.type === 'number'}
                      <input type="number" value={getArg(r.name, p.name)} oninput={(e) => setArg(r.name, p.name, +e.target.value)} />
                    {:else}
                      <input type="text" value={getArg(r.name, p.name)} oninput={(e) => setArg(r.name, p.name, e.target.value)} />
                    {/if}
                  </label>
                {/each}

                <button class="run" class:armed={armed === r.name}
                  disabled={runs[r.name]?.state === 'run'} onclick={() => run(r)}>
                  <Play size={11} strokeWidth={2.5} />
                  {runs[r.name]?.state === 'run' ? 'running…' : armed === r.name ? 'confirm?' : 'Run'}
                </button>

                {#if runs[r.name] && runs[r.name].state !== 'run'}
                  <pre class="result" class:err={runs[r.name].state === 'err'}>{runs[r.name].output}</pre>
                {/if}
              </div>
            {/each}
          {/if}
        </div>
      </aside>
    {/if}

    <nav class="strip" aria-label="RFX packs">
      <div class="strip-mark"><Zap size={12} /></div>
      {#each tabs as t (t.id)}
        {@const TabIcon = rfxIcon(t.icon, Zap)}
        <button class="tab" class:on={openTab === t.id} onclick={() => pick(t.id)}
          aria-label="{t.id} panel" aria-expanded={openTab === t.id}>
          <TabIcon size={13} strokeWidth={2.2} />
          <span class="tab-name">{t.id === '·rfx' ? 'rfx' : t.id}</span>
          <span class="tab-count">{t.count}</span>
        </button>
      {/each}
    </nav>
  </div>
{/if}

<style>
  /* strip participates in layout; the open panel FLOATS over the chat —
     it takes no space from it and carries no backdrop of its own. */
  .npanel { position: relative; display: flex; flex-shrink: 0; min-height: 0; }

  /* ── the strip: always present, ~34px, Blender-N-panel tab rail ── */
  .strip {
    width: 34px; flex-shrink: 0; display: flex; flex-direction: column;
    align-items: stretch; gap: 2px; padding: 8px 0;
    border-left: 1px solid var(--line); background: var(--s1);
  }
  .strip-mark {
    display: flex; justify-content: center; padding: 2px 0 8px;
    color: var(--faint);
  }
  .tab {
    display: flex; flex-direction: column; align-items: center; gap: 5px;
    padding: 9px 0; border: none; cursor: pointer; background: transparent;
    border-left: 2px solid transparent;
    color: var(--dim);
    transition: color .12s, background .12s;
  }
  .tab:hover { color: var(--text); background: color-mix(in srgb, #fff 3.5%, transparent); }
  .tab.on {
    color: var(--accent); background: var(--accent-soft);
    border-left-color: var(--accent);
  }
  .tab-name {
    writing-mode: vertical-rl; transform: rotate(180deg);
    font-family: var(--font-mono, monospace); font-size: 10px; font-weight: 600;
    letter-spacing: .14em;
  }
  .tab-count {
    font-family: var(--font-mono, monospace); font-size: 8.5px;
    color: var(--faint); min-width: 14px; text-align: center;
    padding: 1px 0; border-radius: 4px;
    background: color-mix(in srgb, #fff 4%, transparent);
  }
  .tab.on .tab-count { color: var(--accent); background: transparent; }

  /* ── the panel: a reserved 292px column, visually invisible — no
     background, no border. The chat never gets covered; the opaque cards
     appear to float in the empty space. ── */
  .panel {
    width: 292px; flex-shrink: 0; min-height: 0;
    padding: 8px 8px 8px 0;
    display: flex; flex-direction: column;
    background: transparent;
  }
  .panel-body { overflow-y: auto; display: flex; flex-direction: column; gap: 10px; }
  /* floating cards must be opaque — they sit over chat text, not on a rail */
  .panel-body :global(.pcard), .panel-body .card {
    background: var(--s2);
    box-shadow: 0 0 0 1px var(--ring-strong, var(--line2)), 0 1px 0 0 var(--lift, transparent) inset;
  }

  /* ── default cards (standalone / no-ui reflexes) ── */
  .card {
    padding: 12px; border-radius: 10px;
    background: color-mix(in srgb, #fff 2.5%, transparent); box-shadow: inset 0 0 0 1px var(--line);
  }
  .c-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .c-name { font-size: 12px; font-weight: 650; color: var(--text); font-family: var(--font-mono, monospace); }
  .c-desc { font-size: 11.5px; color: var(--dim); margin: 6px 0 8px; line-height: 1.45; }
  .chip {
    font-size: 9.5px; font-weight: 600; letter-spacing: .04em; padding: 2px 7px; border-radius: 5px;
    color: var(--dim); background: var(--s3); box-shadow: inset 0 0 0 1px var(--line);
  }
  .chip-sensitive { color: var(--warn, #b87a00); }
  .chip-dangerous { color: var(--err); }
  .field { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
  .fname { font-size: 10.5px; color: var(--muted); min-width: 60px; font-family: var(--font-mono, monospace); }
  .field input[type="text"], .field input[type="number"], .field select {
    flex: 1; min-width: 0; font-size: 11.5px; padding: 5px 8px; border-radius: 6px;
    border: 1px solid var(--line); background: var(--bg); color: var(--text); outline: none;
  }
  .field input:focus, .field select:focus { border-color: var(--accent); }
  .run {
    display: inline-flex; align-items: center; gap: 6px; margin-top: 4px;
    font-size: 11px; font-weight: 600; padding: 6px 14px; border: none; border-radius: 7px;
    cursor: pointer; background: var(--accent); color: var(--accent-ink);
  }
  .run.armed { background: var(--err); color: #fff; }
  .run:hover:not(:disabled) { filter: brightness(1.1); }
  .run:disabled { opacity: .5; cursor: wait; }
  .result {
    margin: 8px 0 0; padding: 8px; border-radius: 6px; max-height: 180px; overflow: auto;
    font-size: 10.5px; line-height: 1.5; white-space: pre-wrap; word-break: break-word;
    background: var(--bg); color: var(--text); border: 1px solid var(--line);
    font-family: var(--font-mono, monospace);
  }
  .result.err { border-color: var(--err); color: var(--err); }
</style>
