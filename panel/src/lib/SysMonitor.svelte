<script>
  import { j } from './api.js';
  import { Cpu, Gpu, MemoryStick } from 'lucide-svelte';

  let stats = $state(null);
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
  <!-- compact header cluster -->
  <div class="cluster">
    {#if gpu}
      <div class="chip"><Gpu size={12} /><span class="v">{Math.round(gpu.util)}<span class="u">%</span></span><span class="t {tempTone(gpu.temp)}">{Math.round(gpu.temp)}°</span></div>
    {/if}
    {#if cpu}
      <div class="chip"><Cpu size={12} /><span class="v">{Math.round(cpu.util)}<span class="u">%</span></span><span class="t {tempTone(cpu.temp)}">{Math.round(cpu.temp)}°</span></div>
    {/if}
    {#if ram}
      <div class="chip"><MemoryStick size={12} /><span class="v">{ramPct}<span class="u">%</span></span></div>
    {/if}
  </div>

  <!-- detailed popover -->
  {#if stats}
    <div class="pop">
      <div class="phead"><span class="label">HARDWARE</span></div>

      {#if gpu}
        <div class="comp">
          <div class="crow"><Gpu size={14} /><span class="cname">{gpu.name}</span></div>
          <div class="grid">
            <div class="metric"><span class="mk">TEMP</span><span class="mv {tempTone(gpu.temp)}">{Math.round(gpu.temp)}°C</span></div>
            <div class="metric"><span class="mk">LOAD</span><span class="mv">{Math.round(gpu.util)}%</span></div>
            <div class="metric"><span class="mk">POWER</span><span class="mv">{Math.round(gpu.power)}/{Math.round(gpu.power_max)}W</span></div>
            <div class="metric"><span class="mk">FAN</span><span class="mv">{Math.round(gpu.fan)}%</span></div>
          </div>
          <div class="barrow"><span class="blabel">VRAM</span>
            <div class="bar">{#each Array(10) as _, i}<span class="seg" class:on={i < bars(vramPct)}></span>{/each}</div>
            <span class="bval mono">{(gpu.mem_used/1024).toFixed(1)}/{(gpu.mem_total/1024).toFixed(0)}GB</span>
          </div>
        </div>
      {/if}

      {#if cpu}
        <div class="comp">
          <div class="crow"><Cpu size={14} /><span class="cname">{cpu.name}</span></div>
          <div class="grid">
            <div class="metric"><span class="mk">TEMP</span><span class="mv {tempTone(cpu.temp)}">{Math.round(cpu.temp)}°C</span></div>
            <div class="metric"><span class="mk">LOAD</span><span class="mv">{Math.round(cpu.util)}%</span></div>
            <div class="metric"><span class="mk">THREADS</span><span class="mv">{cpu.cores}</span></div>
          </div>
          <div class="barrow"><span class="blabel">CPU</span>
            <div class="bar">{#each Array(10) as _, i}<span class="seg" class:on={i < bars(Math.round(cpu.util))}></span>{/each}</div>
            <span class="bval mono">{Math.round(cpu.util)}%</span>
          </div>
        </div>
      {/if}

      {#if ram}
        <div class="comp">
          <div class="crow"><MemoryStick size={14} />
            <span class="cname">{ram.vendor ?? 'RAM'} {ram.type ?? ''} {ram.speed ?? ''}{ram.sticks ? ` · ${ram.sticks} sticks` : ''}</span>
          </div>
          <div class="barrow"><span class="blabel">USED</span>
            <div class="bar">{#each Array(10) as _, i}<span class="seg" class:on={i < bars(ramPct)}></span>{/each}</div>
            <span class="bval mono">{(ram.used/1024).toFixed(0)}/{(ram.total/1024).toFixed(0)}GB</span>
          </div>
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .mon { position: relative; display: flex; }

  .cluster { display: flex; align-items: center; gap: 4px; }
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
    width: 320px; z-index: 60;
    background: var(--surface); border-radius: 12px; box-shadow: var(--elev-2);
    padding: 6px;
    opacity: 0; transform: translateY(-4px); pointer-events: none;
    transition: opacity .12s, transform .12s;
  }
  .mon:hover .pop { opacity: 1; transform: none; pointer-events: auto; }
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
