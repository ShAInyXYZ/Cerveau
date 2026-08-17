<script>
  import { Meter, IconButton } from '../kit/index.js';
  import { tooltip } from '../kit/tooltip.js';
  import StatusOrb from './StatusOrb.svelte';
  import SysMonitor from './SysMonitor.svelte';
  import { PanelRight, Database, Smartphone } from 'lucide-svelte';
  import PairDialog from './PairDialog.svelte';
  import logo from './logo.svg?raw'; // inline so it inherits currentColor
  import DotMatrix from './DotMatrix.svelte';

  let {
    health, windowReport,
    activityOpen = $bindable(false), memView = false, onToggleMemory, onMenu = null
  } = $props();

  const components = $derived(health?.components ?? []);
  let pairOpen = $state(false);
  // version comes from the core (/api/health system.version, e.g. "0.3.0-alpha")
  // — hardcoding it here is how the header stayed on V0.2 for a whole release.
  const rev = $derived.by(() => {
    const v = health?.system?.version ?? '';
    const m = v.match(/^(\d+)\.(\d+)/);
    return m ? `V${m[1]}.${m[2]}` : 'V0.3';
  });
</script>

<header class="bar">
  {#if onMenu}
    <button class="hamburger" onclick={onMenu} aria-label="open the session drawer">
      <span></span><span></span><span></span>
    </button>
  {/if}
  <div class="brand">
    <span class="logo">{@html logo}</span>
    <span class="word">CERVEAU</span>
    <span class="rev"><DotMatrix text={rev} dot={1.4} gap={1.3} /></span>
  </div>

  <div class="spacer"></div>

  {#if windowReport}
    <div class="ctx" use:tooltip={`context ${windowReport.tokens}/${windowReport.budget}`}>
      <span class="label">CTX</span>
      <Meter value={windowReport.tokens} max={windowReport.budget} zone={windowReport.zone} />
    </div>
  {/if}

  <span class="sysmon"><SysMonitor /></span>

  <StatusOrb {components} system={health?.system} />

  <IconButton title="pair a phone" active={pairOpen} onclick={() => (pairOpen = true)}>
    <Smartphone size={14} />
  </IconButton>
  <IconButton title="memory browser" active={memView} onclick={onToggleMemory}><Database size={14} /></IconButton>
  <IconButton title="activity" active={activityOpen} onclick={() => (activityOpen = !activityOpen)}>
    <PanelRight size={14} />
  </IconButton>
</header>

<PairDialog bind:open={pairOpen} />

<style>
  .hamburger {
    display: none;
    flex-direction: column; justify-content: center; gap: 3px;
    width: 34px; height: 34px; padding: 8px;
    background: transparent; border: none; cursor: pointer;
  }
  .hamburger span { display: block; height: 1.5px; background: var(--dim); border-radius: 1px; }
  .hamburger:hover span { background: var(--text); }
  @media (max-width: 900px) {
    .hamburger { display: flex; }
  }
  @media (max-width: 640px) {
    /* phone: keep brand + orb + hamburger; the meters and matrix go */
    .sysmon, .rev, .ctx { display: none; }
  }

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
  /* the traced brain logo, accent, sized to the header */
  .brand .logo {
    display: inline-flex; color: var(--accent);
    flex-shrink: 0;
  }
  .brand .logo :global(svg) { width: 22px; height: 22px; display: block; }
  .brand .word { letter-spacing: .3em; }

  /* V0.2 — a bare LED dot-matrix readout, stadium-scoreboard style */
  .brand .rev { display: inline-flex; align-items: center; margin-left: 4px; }

  .spacer { flex: 1; }
  .ctx { display: flex; align-items: center; gap: 7px; }
</style>
