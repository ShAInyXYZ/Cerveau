<script>
  import { Zap, RefreshCw } from 'lucide-svelte';
  import { j, jpost } from '../api';

  // RFX — reflex/talent management (the base UI for RFX)
  let rfx = $state({ packs: [], reflexes: [], notices: [], errors: [] });
  let rfxBusy = $state('');

  async function loadRfx() {
    try { rfx = (await j('/api/rfx')) ?? rfx; } catch { /* core older than RFX */ }
  }
  loadRfx();

  async function toggleRfx(name, enabled) {
    rfxBusy = name;
    try {
      await jpost('/api/rfx/toggle', { name, enabled });
      await loadRfx();
    } finally { rfxBusy = ''; }
  }

  // group reflexes by pack (standalone first)
  const rfxGroups = $derived.by(() => {
    const g = new Map();
    for (const r of rfx.reflexes ?? []) {
      const key = r.pack || '';
      if (!g.has(key)) g.set(key, []);
      g.get(key).push(r);
    }
    const keys = [...g.keys()].sort((a, b) => (a === '' ? -1 : b === '' ? 1 : a.localeCompare(b)));
    const descOf = Object.fromEntries((rfx.packs ?? []).map((p) => [p.name, p]));
    return keys.map((k) => ({ key: k, pack: descOf[k], items: g.get(k) }));
  });
</script>

<section>
  <div class="rfx-head">
    <h2 class="sect-title">RFX Talents</h2>
    <button class="reload" onclick={loadRfx} aria-label="reload reflexes"><RefreshCw size={13} /></button>
  </div>

  {#if (rfx.reflexes ?? []).length === 0}
    <div class="rfx-empty">
      <Zap size={15} />
      <span>No reflexes installed. Drop <code>.rfx.yaml</code> files (or a pack folder) into
      <code>~/.crv/rfx/</code> — or <code>crvcli rfx install</code> one.</span>
    </div>
  {:else}
    {#each rfxGroups as g (g.key)}
      <div class="pack">
        <div class="pack-name">
          {g.key || 'standalone'}
          {#if g.pack}<span class="pack-desc">v{g.pack.version} · {g.pack.description}</span>{/if}
        </div>
        <div class="rows">
          {#each g.items as r (r.name)}
            <div class="row" class:off={!r.enabled}>
              <button
                class="switch"
                class:on={r.enabled}
                disabled={rfxBusy === r.name}
                onclick={() => toggleRfx(r.name, !r.enabled)}
                aria-label={r.enabled ? 'disable ' + r.name : 'enable ' + r.name}
                role="switch" aria-checked={r.enabled}>
                <span class="knob"></span>
              </button>
              <div class="rtext">
                <span class="rname">{r.name}</span>
                <span class="rdesc">{r.description}</span>
              </div>
              <span class="chip" class:chip-dangerous={r.risk === 'dangerous'} class:chip-sensitive={r.risk === 'sensitive'}>{r.risk}</span>
              <span class="chip">{r.kind}</span>
              <span class="chip modes">{(r.modes ?? []).join(' ')}</span>
            </div>
          {/each}
        </div>
      </div>
    {/each}
  {/if}

  {#if (rfx.notices ?? []).length > 0 || (rfx.errors ?? []).length > 0}
    <div class="rfx-issues">
      {#each rfx.notices ?? [] as n}<div class="issue notice">{n}</div>{/each}
      {#each rfx.errors ?? [] as e}<div class="issue err">REJECTED — {e}</div>{/each}
    </div>
  {/if}
  <div class="rfx-foot">Toggles write <code>.state.json</code> — a disabled reflex leaves the grammar on the next turn. Files are never edited.</div>
</section>

<style>
  .rfx-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
  .rfx-head :global(.sect-title) { margin: 0; }
  .reload {
    display: inline-flex; align-items: center; justify-content: center;
    width: 24px; height: 24px; border: none; border-radius: 6px; cursor: pointer;
    background: transparent; color: var(--faint); transition: color .12s, background .12s;
  }
  .reload:hover { color: var(--text); background: var(--s3); }
  .rfx-empty {
    display: flex; align-items: flex-start; gap: 10px;
    padding: 14px 16px; border-radius: 10px; font-size: var(--fs-body); color: var(--dim);
    background: color-mix(in srgb, #fff 2.5%, transparent); box-shadow: inset 0 0 0 1px var(--line);
  }
  .rfx-empty code, .rfx-foot code { font-size: var(--fs-small); color: var(--accent); }
  .pack { margin-bottom: 14px; }
  .pack-name {
    font-size: var(--fs-small); font-weight: 650; letter-spacing: .06em; text-transform: uppercase;
    color: var(--muted); margin: 0 2px 7px; display: flex; align-items: baseline; gap: 10px;
  }
  .pack-desc { font-weight: 400; text-transform: none; letter-spacing: 0; color: var(--faint); }
  .chip {
    flex-shrink: 0; font-size: var(--fs-micro); font-weight: 600; letter-spacing: .04em;
    padding: 3px 8px; border-radius: 6px; color: var(--dim);
    background: var(--s3); box-shadow: inset 0 0 0 1px var(--line);
  }
  .chip-sensitive { color: var(--warn); }
  .chip-dangerous { color: var(--err); }
  .chip.modes { font-family: var(--font-mono); font-weight: 500; }
  .switch {
    flex-shrink: 0; position: relative; width: 34px; height: 19px; border: none; border-radius: 10px;
    cursor: pointer; background: var(--line2); transition: background .15s;
  }
  .switch:disabled { cursor: wait; }
  .switch .knob {
    position: absolute; top: 2.5px; left: 3px; width: 14px; height: 14px; border-radius: 50%;
    background: var(--text); transition: transform .15s;
  }
  .switch.on { background: var(--accent); }
  .switch.on .knob { transform: translateX(14px); }
  .rfx-issues { margin-top: 12px; display: flex; flex-direction: column; gap: 6px; }
  .issue {
    font-size: var(--fs-small); padding: 9px 12px; border-radius: 8px;
    background: color-mix(in srgb, #fff 2.5%, transparent); box-shadow: inset 0 0 0 1px var(--line);
  }
  .issue.notice { color: var(--warn); }
  .issue.err { color: var(--err); }
  .rfx-foot { margin-top: 12px; font-size: var(--fs-small); color: var(--faint); }
</style>
