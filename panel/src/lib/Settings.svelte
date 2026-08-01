<script>
  import { Volume2, VolumeX, Play, Zap, RefreshCw } from 'lucide-svelte';
  import { play, isMuted, setMuted, getVolume, setVolume, getSoundVolume, setSoundVolume, available } from './sound.js';
  import { j, jpost } from './api.js';

  // Settings — deliberately simple. First (and so far only) section: sounds.
  const TYPES = [
    { name: 'done',    label: 'Done',         desc: 'a turn completes successfully' },
    { name: 'error',   label: 'Error',        desc: 'an error card appears' },
    { name: 'ask',     label: 'Ask',          desc: 'the agent asks you a question' },
    { name: 'confirm', label: 'Confirm',      desc: 'a rename or delete succeeds' },
    { name: 'notify',  label: 'Notification', desc: 'a new session is created' }
  ];
  const have = new Set(available());

  let muted = $state(isMuted());
  let master = $state(getVolume());
  let vols = $state(Object.fromEntries(TYPES.map((t) => [t.name, getSoundVolume(t.name)])));

  function toggleMute() { muted = !muted; setMuted(muted); }
  function onMaster(v) { master = v; setVolume(v); }
  function onVol(name, v) { vols[name] = v; setSoundVolume(name, v); }
  // test always audible (force), so you can tune while muted
  function test(name) { play(name, { force: true }); }

  // ── RFX section — reflex/talent management (the base UI for RFX) ──
  let rfx = $state({ packs: [], reflexes: [], notices: [], errors: [] });
  let rfxBusy = $state('');

  async function loadRfx() {
    try { rfx = await j('/api/rfx'); } catch { /* core older than RFX */ }
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

<main class="settings">
  <div class="wrap">
    <div class="shead"><span class="label">SETTINGS</span></div>

    <section>
      <div class="sect-title">Sound</div>

      <!-- master row -->
      <div class="master">
        <button class="mute" class:on={muted} onclick={toggleMute} aria-label={muted ? 'unmute' : 'mute'}>
          {#if muted}<VolumeX size={16} />{:else}<Volume2 size={16} />{/if}
        </button>
        <span class="mlabel">{muted ? 'Muted' : 'Master volume'}</span>
        <input type="range" class="slider" min="0" max="1" step="0.05" value={master}
          disabled={muted} oninput={(e) => onMaster(+e.target.value)} />
        <span class="pct mono">{Math.round(master * 100)}%</span>
      </div>

      <!-- per-sound rows -->
      <div class="rows" class:dim={muted}>
        {#each TYPES as t (t.name)}
          <div class="row">
            <button class="test" disabled={!have.has(t.name)} onclick={() => test(t.name)}
              aria-label="test {t.label}">
              <Play size={12} strokeWidth={2.4} />
            </button>
            <div class="rtext">
              <span class="rname">{t.label}</span>
              <span class="rdesc">{t.desc}</span>
            </div>
            {#if have.has(t.name)}
              <input type="range" class="slider small" min="0" max="1" step="0.05" value={vols[t.name]}
                oninput={(e) => onVol(t.name, +e.target.value)} />
              <span class="pct mono">{Math.round(vols[t.name] * 100)}%</span>
            {:else}
              <span class="missing mono">no file</span>
            {/if}
          </div>
        {/each}
      </div>
    </section>

    <section>
      <div class="sect-title rfx-head">
        <span>RFX Talents</span>
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
  </div>
</main>

<style>
  .settings { flex: 1; overflow-y: auto; display: flex; justify-content: center; }
  .wrap { width: 100%; max-width: 640px; padding: 26px 26px 60px; }
  .shead { margin-bottom: 22px; }

  .sect-title { font-size: 15px; font-weight: 640; color: var(--text); margin-bottom: 14px; }

  /* master volume bar */
  .master {
    display: flex; align-items: center; gap: 12px;
    padding: 13px 16px; border-radius: 12px; margin-bottom: 12px;
    background: var(--surface-raised); box-shadow: var(--elev-1);
  }
  .mute {
    display: inline-flex; align-items: center; justify-content: center;
    width: 32px; height: 32px; border: none; border-radius: 8px; cursor: pointer;
    background: var(--s3); color: var(--muted);
    transition: color .12s, background .12s;
  }
  .mute:hover { color: var(--text); }
  .mute.on { color: var(--err); background: color-mix(in srgb, var(--err) 12%, transparent); }
  .mlabel { font-size: 13px; color: var(--muted); min-width: 96px; }

  /* per-sound rows */
  .rows { display: flex; flex-direction: column; gap: 6px; transition: opacity .15s; }
  .rows.dim { opacity: .45; }
  .row {
    display: flex; align-items: center; gap: 12px;
    padding: 10px 14px; border-radius: 10px;
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--line);
  }
  .test {
    flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center;
    width: 28px; height: 28px; border: none; border-radius: 50%; cursor: pointer;
    background: var(--accent); color: var(--accent-ink);
    transition: filter .1s, transform .05s;
  }
  .test:hover:not(:disabled) { filter: brightness(1.1); }
  .test:active:not(:disabled) { transform: scale(.92); }
  .test:disabled { opacity: .3; cursor: default; }
  .rtext { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
  .rname { font-size: 13px; font-weight: 550; color: var(--text); }
  .rdesc { font-size: 11.5px; color: var(--dim); }
  .missing { font-size: 10px; color: var(--faint); }

  /* sliders — themed range input */
  .slider {
    -webkit-appearance: none; appearance: none;
    flex: 1; max-width: 220px; height: 4px; border-radius: 2px;
    background: var(--line2); outline: none; cursor: pointer;
  }
  .slider.small { max-width: 150px; }
  .slider::-webkit-slider-thumb {
    -webkit-appearance: none; appearance: none;
    width: 14px; height: 14px; border-radius: 50%;
    background: var(--accent); border: none;
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 18%, transparent);
    transition: transform .1s;
  }
  .slider::-webkit-slider-thumb:hover { transform: scale(1.15); }
  .slider::-moz-range-thumb {
    width: 14px; height: 14px; border-radius: 50%;
    background: var(--accent); border: none;
  }
  .slider:disabled { cursor: default; }
  .slider:disabled::-webkit-slider-thumb { background: var(--faint); box-shadow: none; }

  .pct { width: 38px; text-align: right; font-size: 11px; color: var(--dim); font-variant-numeric: tabular-nums; }

  /* ── RFX section ── */
  section { margin-bottom: 30px; }
  .rfx-head { display: flex; align-items: center; justify-content: space-between; }
  .reload {
    display: inline-flex; align-items: center; justify-content: center;
    width: 24px; height: 24px; border: none; border-radius: 6px; cursor: pointer;
    background: transparent; color: var(--faint); transition: color .12s, background .12s;
  }
  .reload:hover { color: var(--text); background: var(--s3); }
  .rfx-empty {
    display: flex; align-items: flex-start; gap: 10px;
    padding: 14px 16px; border-radius: 10px; font-size: 12.5px; color: var(--dim);
    background: color-mix(in srgb, #fff 2.5%, transparent); box-shadow: inset 0 0 0 1px var(--line);
  }
  .rfx-empty code, .rfx-foot code { font-size: 11px; color: var(--accent); }
  .pack { margin-bottom: 14px; }
  .pack-name {
    font-size: 11px; font-weight: 650; letter-spacing: .06em; text-transform: uppercase;
    color: var(--muted); margin: 0 2px 7px; display: flex; align-items: baseline; gap: 10px;
  }
  .pack-desc { font-weight: 400; text-transform: none; letter-spacing: 0; color: var(--faint); }
  .row.off { opacity: .45; }
  .chip {
    flex-shrink: 0; font-size: 10px; font-weight: 600; letter-spacing: .04em;
    padding: 3px 8px; border-radius: 6px; color: var(--dim);
    background: var(--s3); box-shadow: inset 0 0 0 1px var(--line);
  }
  .chip-sensitive { color: var(--amber, #b87a00); }
  .chip-dangerous { color: var(--err); }
  .chip.modes { font-family: var(--mono, monospace); font-weight: 500; }
  .switch {
    flex-shrink: 0; position: relative; width: 34px; height: 19px; border: none; border-radius: 10px;
    cursor: pointer; background: var(--line2); transition: background .15s;
  }
  .switch:disabled { cursor: wait; }
  .switch .knob {
    position: absolute; top: 2.5px; left: 3px; width: 14px; height: 14px; border-radius: 50%;
    background: var(--paper, #faf9f6); transition: transform .15s;
    box-shadow: 0 1px 2px rgba(0,0,0,.3);
  }
  .switch.on { background: var(--accent); }
  .switch.on .knob { transform: translateX(14px); }
  .rfx-issues { margin-top: 12px; display: flex; flex-direction: column; gap: 6px; }
  .issue {
    font-size: 11.5px; padding: 9px 12px; border-radius: 8px;
    background: color-mix(in srgb, #fff 2.5%, transparent); box-shadow: inset 0 0 0 1px var(--line);
  }
  .issue.notice { color: var(--amber, #b87a00); }
  .issue.err { color: var(--err); }
  .rfx-foot { margin-top: 12px; font-size: 11px; color: var(--faint); }
</style>
