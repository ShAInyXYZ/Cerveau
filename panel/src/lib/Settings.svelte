<script>
  import { Volume2, VolumeX, Play } from 'lucide-svelte';
  import { play, isMuted, setMuted, getVolume, setVolume, getSoundVolume, setSoundVolume, available } from './sound.js';

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
</style>
