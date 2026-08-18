<script>
  import { j } from './api';
  import { Cpu, Gpu, MemoryStick } from 'lucide-svelte';
  import HardwareStats from './HardwareStats.svelte';

  // `stats` is bindable so the header can hand the same poll result to the
  // orb's panel on touch — one poll, two renderers, never two pollers.
  let { stats = $bindable(null) } = $props();
  $effect(() => {
    let alive = true;
    const load = async () => { const d = await j('/api/system/stats'); if (alive) stats = d; };
    load();
    const t = setInterval(load, 2000);
    return () => { alive = false; clearInterval(t); };
  });

  const gpu = $derived(stats?.gpu);
  const cpu = $derived(stats?.cpu);
  const ram = $derived(stats?.ram);

  function tempTone(t) { return t >= 85 ? 'hot' : t >= 72 ? 'warm' : 'cool'; }
  function pct(u, t) { return t > 0 ? Math.round((u / t) * 100) : 0; }
  const ramPct = $derived(ram ? pct(ram.used, ram.total) : 0);
  const vramPct = $derived(gpu ? pct(gpu.mem_used, gpu.mem_total) : 0);

  // compact bar segments
  function bars(p) { return Math.round((p / 100) * 10); }
</script>

<div class="mon">
  <!-- compact header cluster. A button, not a div: hover alone cannot open
       this on a touch screen, and a real button also makes it keyboard
       reachable rather than mouse-only. -->
  <button class="cluster" aria-label="system status" aria-expanded="false">
    {#if gpu}
      <div class="chip"><Gpu size={12} /><span class="v">{Math.round(gpu.util)}<span class="u">%</span></span><span class="t {tempTone(gpu.temp)}">{Math.round(gpu.temp)}°</span></div>
    {/if}
    {#if cpu}
      <div class="chip"><Cpu size={12} /><span class="v">{Math.round(cpu.util)}<span class="u">%</span></span><span class="t {tempTone(cpu.temp)}">{Math.round(cpu.temp)}°</span></div>
    {/if}
    {#if ram}
      <div class="chip"><MemoryStick size={12} /><span class="v">{ramPct}<span class="u">%</span></span></div>
    {/if}
  </button>

  <!-- detailed popover -->
  {#if stats}
    <div class="pop">
      <div class="phead"><span class="label">HARDWARE</span></div>
      <HardwareStats {stats} />
    </div>
  {/if}
</div>

<style>
  .mon { position: relative; display: flex; }

  .cluster {
    display: flex; align-items: center; gap: 4px;
    background: none; border: none; padding: 0; margin: 0;
    font: inherit; color: inherit; cursor: pointer;
  }
  .cluster:focus-visible { outline: 2px solid var(--accent-line); outline-offset: 3px; border-radius: 8px; }
  .chip {
    display: inline-flex; align-items: center; gap: 5px;
    height: 30px; padding: 0 9px;
    border-radius: 8px;
    background: var(--surface-raised); box-shadow: var(--elev-1);
    color: var(--dim);
  }
  .chip .v { font-family: var(--font-mono); font-size: 11px; color: var(--muted); }
  .chip .u { color: var(--faint); font-size: 9px; }
  .chip .t {
    font-family: var(--font-mono); font-size: 9px;
    padding: 1px 5px; border-radius: 999px;
  }
  .t.cool { color: var(--ok); background: color-mix(in srgb, var(--ok) 12%, transparent); }
  .t.warm { color: var(--warn); background: color-mix(in srgb, var(--warn) 12%, transparent); }
  .t.hot  { color: var(--err); background: color-mix(in srgb, var(--err) 14%, transparent); }

  /* popover */
  .pop {
    position: absolute; top: calc(100% + 8px); right: 0;
    width: 320px; z-index: var(--z-popover);
    background: var(--surface); border-radius: 12px; box-shadow: var(--elev-2);
    padding: 6px;
    opacity: 0; transform: translateY(-4px); pointer-events: none;
    transition: opacity .12s, transform .12s;
  }
  .mon:hover .pop { opacity: 1; transform: none; pointer-events: auto; }

  /* Touch: three metric chips consumed the whole bar and pushed the icon
     buttons off-screen. Collapse to ONE chip — the GPU, the number that
     actually moves during a run — and put the rest in the popover, which is
     a tap away. The chips are a glance affordance, not the data itself. */
  @media (max-width: 720px), (pointer: coarse) {
    .cluster .chip:not(:first-child) { display: none; }
    .chip { height: 26px; padding: 0 7px; }
  }

  /* On a narrow screen the trigger sits near the right edge, so a 320px
     panel anchored to it runs off the LEFT of the viewport — the component
     labels were being clipped mid-word. Detach from the trigger and pin it
     to the screen instead, sized to whatever room actually exists. */
  @media (max-width: 720px), (pointer: coarse) {
    .pop {
      position: fixed;
      top: calc(var(--bar-h, 46px) + 6px);
      right: 8px; left: 8px;
      width: auto; max-width: 360px; margin-left: auto;
      max-height: calc(100dvh - var(--bar-h, 46px) - 24px);
      overflow-y: auto; overscroll-behavior: contain;
    }
  }

  /* Touch has no hover, so the panel could never be opened by tapping —
     :focus-within lets a tap on the (focusable) trigger reveal it. */
  .mon:focus-within .pop { opacity: 1; transform: none; pointer-events: auto; }
  .phead { padding: 8px 10px 6px; }

  .comp { padding: 10px; border-radius: 8px; }
  .comp + .comp { border-top: 1px solid var(--line); }
  .crow { display: flex; align-items: center; gap: 8px; color: var(--muted); margin-bottom: 9px; }
  .cname { font-size: 11.5px; color: var(--text); font-weight: 550; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .grid { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 9px; }
  .metric {
    display: flex; flex-direction: column; gap: 2px;
    padding: 6px 9px; border-radius: 6px; min-width: 62px;
    background: color-mix(in srgb, #fff 2.5%, transparent);
    box-shadow: inset 0 0 0 1px var(--ring);
  }
  .mk { font-family: var(--font-mono); font-size: 8px; letter-spacing: .12em; color: var(--faint); }
  .mv { font-family: var(--font-mono); font-size: 12px; color: var(--text); }
  .mv.cool { color: var(--ok); } .mv.warm { color: var(--warn); } .mv.hot { color: var(--err); }

  .barrow { display: flex; align-items: center; gap: 9px; }
  .blabel { font-family: var(--font-mono); font-size: 8.5px; letter-spacing: .12em; color: var(--faint); width: 34px; }
  .bar { flex: 1; display: flex; gap: 2px; }
  .seg { flex: 1; height: 6px; border-radius: 1px; background: var(--line2); }
  .seg.on { background: var(--accent); }
  .bval { font-size: 9.5px; color: var(--dim); flex-shrink: 0; }
</style>
