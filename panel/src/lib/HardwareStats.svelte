<script>
  import { Cpu, Gpu, MemoryStick } from 'lucide-svelte';

  // The GPU/CPU/RAM readout. Extracted so the desktop's own hover popover and
  // the phone's merged system panel render the SAME markup — a phone-only copy
  // would drift the moment either one changed.
  let { stats = null } = $props();

  const gpu = $derived(stats?.gpu);
  const cpu = $derived(stats?.cpu);
  const ram = $derived(stats?.ram);

  function tempTone(t) { return t >= 85 ? 'hot' : t >= 72 ? 'warm' : 'cool'; }
  function pct(u, t) { return t > 0 ? Math.round((u / t) * 100) : 0; }
  const ramPct = $derived(ram ? pct(ram.used, ram.total) : 0);
  const vramPct = $derived(gpu ? pct(gpu.mem_used, gpu.mem_total) : 0);
  function bars(p) { return Math.round((p / 100) * 10); }
</script>

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

<style>
  .comp { padding: 10px; border-radius: 8px; }
  .comp + .comp { border-top: 1px solid var(--line); }
  .crow { display: flex; align-items: center; gap: 8px; color: var(--muted); margin-bottom: 9px; }
  .cname { font-size: 12px; }
  .grid { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 9px; }
  .metric {
    display: flex; flex-direction: column; gap: 2px;
    padding: 6px 9px; border-radius: 7px;
    background: var(--s2); min-width: 62px;
  }
  .mk { font-size: 8.5px; letter-spacing: .12em; color: var(--faint); }
  .mv { font-family: var(--font-mono); font-size: 13px; color: var(--text); }
  .mv.cool { color: var(--ok); }
  .mv.warm { color: var(--warn); }
  .mv.hot  { color: var(--err); }
  .barrow { display: flex; align-items: center; gap: 8px; }
  .blabel { font-size: 8.5px; letter-spacing: .12em; color: var(--faint); min-width: 34px; }
  .bar { display: flex; gap: 2px; flex: 1; }
  .seg { flex: 1; height: 7px; border-radius: 2px; background: var(--s3); }
  .seg.on { background: var(--accent); }
  .bval { font-size: 10.5px; color: var(--muted); }
</style>
