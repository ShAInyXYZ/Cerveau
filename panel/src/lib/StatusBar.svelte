<script>
  import { Meter, IconButton } from '../kit/index.js';
  import { tooltip } from '../kit/tooltip.js';
  import StatusOrb from './StatusOrb.svelte';
  import SysMonitor from './SysMonitor.svelte';
  import { PanelRight, Database } from 'lucide-svelte';
  import logo from './logo.svg?raw'; // inline so it inherits currentColor
  import DotMatrix from './DotMatrix.svelte';

  let {
    health, windowReport,
    activityOpen = $bindable(false), memView = false, onToggleMemory
  } = $props();

  const components = $derived(health?.components ?? []);
</script>

<header class="bar">
  <div class="brand">
    <span class="logo">{@html logo}</span>
    <span class="word">CERVEAU</span>
    <span class="rev"><DotMatrix text="V0.1" dot={1.4} gap={1.3} /></span>
  </div>

  <div class="spacer"></div>

  {#if windowReport}
    <div class="ctx" use:tooltip={`context ${windowReport.tokens}/${windowReport.budget}`}>
      <span class="label">CTX</span>
      <Meter value={windowReport.tokens} max={windowReport.budget} zone={windowReport.zone} />
    </div>
  {/if}

  <SysMonitor />

  <StatusOrb {components} system={health?.system} />

  <IconButton title="memory browser" active={memView} onclick={onToggleMemory}><Database size={14} /></IconButton>
  <IconButton title="activity" active={activityOpen} onclick={() => (activityOpen = !activityOpen)}>
    <PanelRight size={14} />
  </IconButton>
</header>

<style>
  .bar {
    display: flex; align-items: center; gap: 14px;
    height: 46px; flex-shrink: 0;
    padding: 0 12px;
    border-bottom: 1px solid var(--line);
    background: var(--s1);
  }
  .brand {
    display: flex; align-items: center; gap: 9px;
    font-family: var(--font-mono); font-weight: 600;
    font-size: 13px; color: var(--text);
  }
  /* the traced brain logo, amber, sized to the header */
  .brand .logo {
    display: inline-flex; color: var(--accent);
    flex-shrink: 0;
  }
  .brand .logo :global(svg) { width: 22px; height: 22px; display: block; }
  .brand .word { letter-spacing: .3em; }

  /* V0.1 — a bare LED dot-matrix readout, stadium-scoreboard style */
  .brand .rev { display: inline-flex; align-items: center; margin-left: 4px; }

  .spacer { flex: 1; }
  .ctx { display: flex; align-items: center; gap: 7px; }
</style>
