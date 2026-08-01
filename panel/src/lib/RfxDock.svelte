<script>
  import { Zap, Play, ChevronRight, RefreshCw } from 'lucide-svelte';
  import { j, jpost } from './api.js';
  import RfxPackCard from './RfxPackCard.svelte';

  // RfxDock — the RFX chat dock (docs/RFX-UI.md). Pack cockpits first
  // (ui: manifest), default quick-run cards for everything else.
  let data = $state({ packs: [], reflexes: [] });
  let runs = $state({});
  let args = $state({});
  let collapsed = $state(localStorage.getItem('rfxdock-collapsed') === '1');

  async function load() {
    try { data = await j('/api/rfx'); } catch { /* older core */ }
  }
  load();

  const enabled = $derived((data.reflexes ?? []).filter((r) => r.enabled));
  const uiPacks = $derived((data.packs ?? []).filter((p) => (p.ui?.widgets ?? []).length > 0));
  const membersOf = $derived((name) => enabled.filter((r) => r.pack === name));
  // default cards: standalone reflexes, or reflexes whose pack declares no ui
  const defaults = $derived(enabled.filter((r) => !r.pack || !uiPacks.some((p) => p.name === r.pack)));

  function getArg(name, p) { return args[name]?.[p] ?? ''; }
  function setArg(name, p, v) { (args[name] ??= {})[p] = v; }

  function toggleCollapsed() {
    collapsed = !collapsed;
    localStorage.setItem('rfxdock-collapsed', collapsed ? '1' : '0');
  }

  function paramsOf(r) {
    const props = r.params?.properties ?? {};
    const required = new Set(r.params?.required ?? []);
    return Object.entries(props).map(([name, s]) => ({ name, required: required.has(name), ...(s ?? {}) }));
  }

  async function run(r) {
    const a = {};
    for (const p of paramsOf(r)) {
      const v = args[r.name]?.[p.name];
      if (v !== undefined && v !== '') a[p.name] = v;
    }
    runs[r.name] = { state: 'run', output: '' };
    try {
      const res = await jpost('/api/rfx/run', { name: r.name, args: a });
      runs[r.name] = res.ok ? { state: 'ok', output: res.output || '(no output)' }
                            : { state: 'err', output: (res.output ? res.output + '\n' : '') + res.error };
    } catch (e) {
      runs[r.name] = { state: 'err', output: String(e) };
    }
  }
</script>

{#if enabled.length > 0}
  {#if collapsed}
    <button class="strip" onclick={toggleCollapsed} aria-label="open RFX dock">
      <Zap size={14} /><span class="strip-count">{enabled.length}</span>
    </button>
  {:else}
    <aside class="dock">
      <div class="dock-head">
        <span class="label">RFX · {enabled.length}</span>
        <div class="dock-head-btns">
          <button class="icon" onclick={load} aria-label="reload"><RefreshCw size={12} /></button>
          <button class="icon" onclick={toggleCollapsed} aria-label="collapse"><ChevronRight size={13} /></button>
        </div>
      </div>

      <div class="cards">
        {#each uiPacks as p (p.name)}
          {#if membersOf(p.name).length > 0}
            <RfxPackCard pack={p} members={membersOf(p.name)} />
          {/if}
        {/each}

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

            <button class="run" disabled={runs[r.name]?.state === 'run'} onclick={() => run(r)}>
              <Play size={11} strokeWidth={2.5} />
              {runs[r.name]?.state === 'run' ? 'running…' : 'Run'}
            </button>

            {#if runs[r.name] && runs[r.name].state !== 'run'}
              <pre class="result" class:err={runs[r.name].state === 'err'}>{runs[r.name].output}</pre>
            {/if}
          </div>
        {/each}
      </div>
    </aside>
  {/if}
{/if}

<style>
  .strip {
    display: flex; flex-direction: column; align-items: center; gap: 6px;
    width: 30px; padding: 10px 0; border: none; cursor: pointer;
    background: var(--surface-raised); border-left: 1px solid var(--line); color: var(--accent);
  }
  .strip-count { font-size: 10px; color: var(--dim); }
  .dock {
    width: 292px; flex-shrink: 0; display: flex; flex-direction: column;
    border-left: 1px solid var(--line); background: var(--surface-raised); overflow: hidden;
  }
  .dock-head {
    display: flex; align-items: center; justify-content: space-between;
    padding: 10px 12px; border-bottom: 1px solid var(--line);
  }
  .dock-head-btns { display: flex; gap: 4px; }
  .icon {
    display: inline-flex; align-items: center; justify-content: center;
    width: 22px; height: 22px; border: none; border-radius: 6px; cursor: pointer;
    background: transparent; color: var(--faint);
  }
  .icon:hover { color: var(--text); background: var(--s3); }
  .cards { flex: 1; overflow-y: auto; padding: 10px; display: flex; flex-direction: column; gap: 10px; }
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
