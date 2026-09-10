<script>
 import { sessionStore } from './stores/session.svelte.ts';
  import { onMount, onDestroy } from 'svelte';
  import { Play, Zap } from 'lucide-svelte';
  import { rfxIcon } from './rfxIcons.js';
  import { j, jpost } from './api';
  import RfxPackCard from './RfxPackCard.svelte';
  import RfxCustomPanel from './RfxCustomPanel.svelte';
  import { rfxParams, parseRfxArgs, RfxRequestScope, contextImages, attachContextImage } from './rfxArgs';

  // RfxDock — Blender-N-panel-style RFX surface (docs-private/RFX-UI.md).
  // A slim vertical tab strip lives at the chat's right edge — one rotated
  // tab per pack (plus one for standalone reflexes). Clicking a tab expands
  // that pack's panel INLINE next to the strip; clicking it again collapses
  // back to the bare strip. Only one pack is open at a time.
  let { sessionId = null, onTurn = null } = $props();
  let data = $state({ packs: [], reflexes: [] });
  let runs = $state({});
  let args = $state({});
  let armed = $state('');            // name of the dangerous reflex awaiting confirm
  let pending = $state('');
  let loadError = $state('');
  let loading = $state(true);
  const scope = new RfxRequestScope();
  const sid = $derived(sessionId ?? sessionStore.activeId);
  let disposed = false, loadSequence = 0, armTimer;
  // ?rfx=<pack> deep-links a panel open (also how CI screenshots verify it)
  function initialTab() { try { return new URLSearchParams(location.search).get('rfx') ?? localStorage.getItem('rfxdock-tab') ?? ''; } catch { return ''; } }
  let openTab = $state(initialTab());

  async function load() {
    const sequence = ++loadSequence;
    loading = true;
    try {
      const response = await j('/api/rfx');
      if (disposed || sequence !== loadSequence) return;
      if (!response || !Array.isArray(response.packs) || !Array.isArray(response.reflexes)) throw new Error('RFX catalog is unavailable. Check the connection and retry.');
      data = response; loadError = '';
    } catch (error) { if (!disposed && sequence === loadSequence) loadError = String(error); }
    finally { if (!disposed && sequence === loadSequence) loading = false; }
  }
  onMount(() => { void load(); });
  onDestroy(() => { disposed = true; loadSequence++; scope.dispose(); clearTimeout(armTimer); });
  $effect(() => {
    sid;
    scope.invalidate(); runs = {}; args = {}; armed = ''; pending = '';
    clearTimeout(armTimer);
  });

  const enabled = $derived((data.reflexes ?? []).filter((r) => r.enabled));
  const uiPacks = $derived((data.packs ?? []).filter((p) => (p.ui?.widgets ?? []).length > 0 || p.has_panel));
  const membersOf = $derived((name) => enabled.filter((r) => r.pack === name));
  // the card gets ALL members — it greys buttons whose target is disabled
  const allMembersOf = $derived((name) => (data.reflexes ?? []).filter((r) => r.pack === name));
  // standalone group: reflexes with no pack, or whose pack declares no ui
  const defaults = $derived((data.reflexes ?? []).filter((r) => !r.pack || !uiPacks.some((p) => p.name === r.pack)));

  // tabs: packs with a live panel + one "rfx" tab for the standalone group
  const tabs = $derived([
    // a UI-only pack (panel, zero talents) is a supervisor — it still gets a tab
    ...uiPacks.filter((p) => allMembersOf(p.name).length > 0 || p.ui_only)
      .map((p) => ({ id: p.name, count: membersOf(p.name).length, icon: p.icon })),
    ...(defaults.length ? [{ id: '·rfx', count: defaults.length, icon: 'zap' }] : [])
  ]);
  const openPack = $derived(uiPacks.find((p) => p.name === openTab));
  const openValid = $derived(tabs.some((t) => t.id === openTab));

  function pick(id) {
    openTab = openTab === id ? '' : id;
    try { localStorage.setItem('rfxdock-tab', openTab); } catch { /* storage is optional */ }
  }

  function getArg(name, p) { return args[name]?.[p] ?? ''; }
  function setArg(name, p, v) { (args[name] ??= {})[p] = v; armed = ''; }

  function paramsOf(r) {
    return rfxParams(r.params);
  }

  // Dangerous tier gets a two-click arm/confirm — a human misclick should
  // not fire a dangerous reflex, and neither should a stray local request
  // decide it's "just a button".
  async function run(r) {
    if (!sid || !r.enabled || pending || sessionStore.running || sessionStore.connectionLost) return;
    let a;
    try { a = parseRfxArgs(r.params, args[r.name]); }
    catch (error) { runs[r.name] = { state: 'err', output: String(error) }; return; }
    if (r.risk === 'dangerous' && armed !== r.name) {
      armed = r.name;
      clearTimeout(armTimer);
      armTimer = setTimeout(() => { if (armed === r.name) armed = ''; }, 5000);
      return;
    }
    const confirmed = r.risk === 'dangerous';
    armed = '';
    const token = scope.begin(sid);
    if (!token) { runs[r.name] = { state: 'err', output: 'Another RFX request is awaiting acknowledgement in this session.' }; return; }
    pending = r.name;
    runs[r.name] = { state: 'run', output: '' };
    try {
      const res = await jpost('/api/rfx/run', { session_id: token.sessionId, name: r.name, args: a, confirmed });
      if (!scope.current(token, sid)) return;
      runs[r.name] = res.ok ? { state: 'ok', output: res.output || '(no output)' }
                            : { state: 'err', output: (res.output ? res.output + '\n' : '') + res.error };
    } catch (e) {
      if (scope.current(token, sid)) runs[r.name] = { state: 'err', output: String(e) };
    } finally { scope.finish(token); if (scope.current(token, sid)) pending = ''; }
  }
</script>

{#snippet talentList(talents)}
  {#each talents as r (r.name)}
    <div class="card">
      <div class="c-head"><span class="c-name">{r.name}</span><span class="chip" class:chip-dangerous={r.risk === 'dangerous'} class:chip-sensitive={r.risk === 'sensitive'}>{r.risk}</span></div>
      <div class="c-desc">{r.description}</div>
      {#if !r.enabled}<p class="hint">Disabled in RFX settings.</p>{/if}
      {#each paramsOf(r) as p (p.name)}
        <label class="field" class:structured={p.type === 'array' || p.type === 'object'}>
          <span class="fname">{p.name}{p.isRequired ? ' *' : ''}</span>
          {#if p.enum}
            <select value={getArg(r.name, p.name)} onchange={(e) => setArg(r.name, p.name, e.target.value)}>
              <option value="">Choose…</option>{#each p.enum as value}<option value={String(value)}>{String(value)}</option>{/each}
            </select>
          {:else if p.type === 'boolean'}
            <select value={String(getArg(r.name, p.name))} onchange={(e) => setArg(r.name, p.name, e.target.value)}><option value="">Not set</option><option value="true">True</option><option value="false">False</option></select>
          {:else if p.type === 'array' || p.type === 'object'}
            <textarea rows="3" value={getArg(r.name, p.name)} oninput={(e) => setArg(r.name, p.name, e.target.value)} placeholder={p.type === 'array' ? '["path/to/file"]' : '{"key":"value"}'} spellcheck="false"></textarea>
          {:else if p.type === 'integer' || p.type === 'number'}
            <input type="number" value={getArg(r.name, p.name)} step={p.type === 'integer' ? 1 : 'any'} min={p.minimum} max={p.maximum} oninput={(e) => setArg(r.name, p.name, e.target.value)} />
          {:else}
            <input type="text" value={getArg(r.name, p.name)} oninput={(e) => setArg(r.name, p.name, e.target.value)} />
          {/if}
          {#if p.description}<span class="field-help">{p.description}</span>{/if}
        </label>
      {/each}
      <button class="run" class:armed={armed === r.name} disabled={!r.enabled || !!pending || !sid || sessionStore.running || sessionStore.connectionLost} onclick={() => run(r)}>
        <Play size={11} strokeWidth={2.5} />{pending === r.name ? 'Running…' : armed === r.name ? 'Confirm run' : 'Run'}
      </button>
      {#if armed === r.name}<span class="hint">Dangerous action. Click Confirm run within 5 seconds.</span>{/if}
      {#if runs[r.name] && runs[r.name].state !== 'run'}
        <pre class="result" class:err={runs[r.name].state === 'err'} role={runs[r.name].state === 'err' ? 'alert' : undefined}>{runs[r.name].output}</pre>
        {#if runs[r.name].state === 'ok'}
          {#each contextImages(runs[r.name].output) as image (image.path)}
            <button class="attach" disabled={sessionStore.running || sessionStore.connectionLost} onclick={() => attachContextImage(image, sid)}>Attach to chat · {image.width}×{image.height}</button>
          {/each}
        {/if}
      {/if}
    </div>
  {/each}
{/snippet}

{#if loading || loadError || tabs.length > 0}
  <div class="npanel">
    {#if openValid && openTab}
      <aside class="panel anim-rise">
        <div class="panel-body">
          {#if openPack?.has_panel}
            <RfxCustomPanel pack={openPack} members={allMembersOf(openPack.name)} {sessionId} {onTurn} />
          {:else if openPack}
            <RfxPackCard pack={openPack} members={allMembersOf(openPack.name)} sessionId={sid} />
          {/if}
          {#if !sid}<p class="hint">Select a session before running a talent.</p>
          {:else if sessionStore.running}<p class="hint">A run owns this session. RFX actions are available when it finishes.</p>
          {:else if sessionStore.connectionLost}<p class="hint">Session connection unavailable. Wait for reconnection before running a talent.</p>{/if}
          {#if openPack && allMembersOf(openPack.name).length}
            <details class="all-actions"><summary>All actions ({allMembersOf(openPack.name).length})</summary>{@render talentList(allMembersOf(openPack.name))}</details>
          {:else if !openPack}
            {@render talentList(defaults)}
          {/if}
        </div>
      </aside>
    {/if}

    <nav class="strip" aria-label="RFX packs">
      <div class="strip-mark"><Zap size={12} /></div>
      {#if loading}<span class="load-state" role="status">Loading RFX…</span>{/if}
      {#if loadError}<button class="load-error" onclick={load} title={loadError}>Retry RFX</button>{/if}
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
    {#if loadError}<p class="catalog-error" role="alert">{loadError}</p>{/if}
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
    background: var(--floating-surface); backdrop-filter: var(--floating-blur); border-radius: var(--r-panel);
  }
  .panel-body { overflow-y: auto; display: flex; flex-direction: column; gap: 10px; }
  .all-actions { min-width: 0; }
  .all-actions summary { cursor: pointer; color: var(--muted); font-size: 12px; padding: 10px 4px; }
  .all-actions .card + .card { margin-top: 8px; }
  .hint, .field-help { color: var(--muted); font-size: 11px; line-height: 1.45; overflow-wrap: anywhere; }
  .field-help { flex-basis: 100%; }
  .hint { display: block; margin: 4px 0; }
  .load-state, .load-error { font-size: 11px; color: var(--muted); }
  .load-error { cursor: pointer; background: var(--s2); border: 1px solid var(--line); }
  .catalog-error { max-width: 260px; align-self: flex-start; color: var(--err); font-size: 12px; padding: 8px; overflow-wrap: anywhere; }
  .attach { margin-top: 8px; padding: 7px 10px; background: var(--s3); color: var(--text); border: 1px solid var(--line2); border-radius: var(--r); cursor: pointer; font-size: 12px; }
  /* floating cards must be opaque — they sit over chat text, not on a rail */
  .panel-body :global(.pcard), .panel-body .card {
    background: transparent;
    box-shadow: none;
  }

  /* ── default cards (standalone / no-ui reflexes) ── */
  .card {
    padding: 12px; border-radius: 10px;
    background: color-mix(in srgb, #fff 2.5%, transparent); box-shadow: inset 0 0 0 1px var(--line);
  }
  .c-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .c-name { min-width: 0; overflow-wrap: anywhere; font-size: 12px; font-weight: 650; color: var(--text); font-family: var(--font-mono, monospace); }
  .c-desc { font-size: 11.5px; color: var(--dim); margin: 6px 0 8px; line-height: 1.45; }
  .chip {
    font-size: 9.5px; font-weight: 600; letter-spacing: .04em; padding: 2px 7px; border-radius: 5px;
    color: var(--dim); background: var(--s3); box-shadow: inset 0 0 0 1px var(--line);
  }
  .chip-sensitive { color: var(--warn, #b87a00); }
  .chip-dangerous { color: var(--err); }
  .field { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-bottom: 8px; }
  .fname { font-size: 10.5px; color: var(--muted); min-width: 60px; font-family: var(--font-mono, monospace); }
  .field input[type="text"], .field input[type="number"], .field select, .field textarea {
    flex: 1; min-width: 0; font-size: 11.5px; padding: 5px 8px; border-radius: 6px;
    border: 1px solid var(--line); background: var(--bg); color: var(--text); outline: none;
  }
  .field textarea { flex-basis: 100%; resize: vertical; font-family: var(--font-mono, monospace); }
  .field input:focus, .field select:focus, .field textarea:focus { border-color: var(--accent); }
  button:focus-visible, summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .run {
    display: inline-flex; align-items: center; gap: 6px; margin-top: 4px;
    font-size: 11px; font-weight: 600; padding: 6px 14px; border: none; border-radius: 7px;
    cursor: pointer; background: var(--accent); color: var(--on-accent);
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
  @media (max-width: 900px) {
    .npanel { flex-direction: column; width: 100%; max-height: min(55dvh, 520px); }
    .strip { order: -1; width: 100%; flex-direction: row; overflow-x: auto; align-items: center; padding: 4px 8px; border-left: 0; border-top: 1px solid var(--line); }
    .strip-mark { padding: 6px; }
    .tab { flex-direction: row; flex-shrink: 0; min-height: 40px; padding: 8px 10px; border-left: 0; border-bottom: 1px solid transparent; }
    .tab.on { border-bottom-color: var(--accent); }
    .tab-name { writing-mode: horizontal-tb; transform: none; letter-spacing: .04em; font-size: 12px; }
    .tab-count { font-size: 11px; }
    .panel { width: 100%; max-height: calc(55dvh - 52px); padding: 8px; flex: 1 1 auto; }
    .field input[type="text"], .field input[type="number"], .field select, .field textarea { font-size: 16px; }
    .run, .attach { min-height: 40px; }
    .catalog-error { max-width: none; }
  }
</style>
