<script>
  import { Meter, IconButton } from '../kit/index.js';
  import { tooltip } from '../kit/tooltip.js';
  import StatusOrb from './StatusOrb.svelte';
  import SysMonitor from './SysMonitor.svelte';
  import { PanelRight, Database, MonitorSmartphone } from 'lucide-svelte';
  import PairDialog from './PairDialog.svelte';
  import logo from './logo.svg?raw'; // inline so it inherits currentColor
  import DotMatrix from './DotMatrix.svelte';

  let {
    health, windowReport,
    activityOpen = $bindable(false), memView = false, onToggleMemory
  } = $props();

  // The hardware poll lives in SysMonitor; the orb renders the same result
  // on touch. `display: none` does NOT stop a Svelte $effect, so the poll keeps
  // running even when the chips themselves are hidden on a phone.
  let sysStats = $state(null);

  // Half-size matrix on a phone. The full-size readout runs into the camera
  // cutout — content now flows into that region, so it has to be smaller
  // rather than merely positioned around it. Measured, not media-queried:
  // this device does not match the width/pointer queries reliably.
  const coarse = typeof matchMedia === 'function'
    && matchMedia('(pointer: coarse)').matches;
  const revDot = coarse ? 0.7 : 1.4;
  const revGap = coarse ? 0.65 : 1.3;

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
  <div class="brand">
    <span class="logo">{@html logo}</span>
    <span class="word">CERVEAU</span>
    <span class="rev"><DotMatrix text={rev} dot={revDot} gap={revGap} /></span>
  </div>

  <div class="spacer"></div>

  {#if windowReport}
    <div class="ctx" use:tooltip={`context ${windowReport.tokens}/${windowReport.budget}`}>
      <span class="label">CTX</span>
      <Meter value={windowReport.tokens} max={windowReport.budget} zone={windowReport.zone} />
    </div>
  {/if}

  <span class="sysmon"><SysMonitor bind:stats={sysStats} /></span>

  <StatusOrb {components} system={health?.system} stats={sysStats} />

  <IconButton title="pair a device" active={pairOpen} onclick={() => (pairOpen = true)}>
    <MonitorSmartphone size={14} />
  </IconButton>
  <IconButton title="memory browser" active={memView} onclick={onToggleMemory}><Database size={14} /></IconButton>
  <IconButton title="activity" active={activityOpen} onclick={() => (activityOpen = !activityOpen)}>
    <PanelRight size={14} />
  </IconButton>
</header>

<PairDialog bind:open={pairOpen} />

<style>
  /* Phone: keep brand + orb + controls. The dot-matrix version and the CTX
     meter are desktop affordances — on a phone they crowd the icon run that
     actually gets tapped. 640px was too narrow a cutoff: a 1080px-wide phone
     still reports well above it, so this keys off pointer type instead. */
  @media (max-width: 640px), (pointer: coarse) {
    /* .sysmon deliberately stays: the stack readout is wanted on the phone,
       and its popover is now viewport-aware. Only the decorative version
       matrix and the CTX meter go. */
    /* the metric chips fold into the orb's panel on touch — see StatusOrb */
    .ctx, .sysmon { display: none; }
  }

  .bar {
    display: flex; align-items: center; gap: 14px;
    height: 46px; flex-shrink: 0;
    padding: 0 12px;
    border-bottom: 1px solid var(--line);
    background: var(--s1);
  }
  /* Touch overrides. These MUST come after the `.bar` base rule above:
     same specificity means source order decides, and sitting before it the
     gap override simply lost the cascade and never applied.
     Keyed on pointer type rather than width — this phone reports wider than
     720px, so a width-only query never fired (same trap as the old 640px
     breakpoint). */
  @media (max-width: 720px), (pointer: coarse) {
    .bar { gap: 6px; padding: 0 8px; }
    .bar :global(.ib) { margin-left: -4px; }
    /* the first button keeps clear of the status orb */
    .bar :global(.ib:first-of-type) { margin-left: 0; }
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
