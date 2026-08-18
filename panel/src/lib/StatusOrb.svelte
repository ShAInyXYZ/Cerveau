<script>
  import HardwareStats from './HardwareStats.svelte';

  // `stats` arrives from the header's SysMonitor, which owns the poll. On a
  // phone the hardware readout is folded INTO this panel: two separate
  // popovers competing for one narrow bar was the crowding problem.
  let { components = [], system = null, stats = null } = $props();

  const anyDown = $derived(components.some((c) => !c.ok));
  const tone = $derived(components.length === 0 ? 'off' : anyDown ? 'err' : 'ok');
</script>

<div class="orbwrap">
  <!-- simplified trigger: just the status ring -->
  <button class="orb {tone}" aria-label="system status">
    <span class="core"></span>
  </button>

  <div class="pop">
    <div class="phead">
      <span class="label">SYSTEM</span>
      <span class="pspace"></span>
      {#if system?.version}<span class="tag">v{system.version}</span>{/if}
      {#if system?.uptime}<span class="tag up">↑ {system.uptime}</span>{/if}
    </div>

    <div class="rows">
      {#each components as c}
        <div class="row">
          <span class="dot {c.ok ? 'ok' : 'err'}"></span>
          <span class="cname">{c.name}</span>
          <span class="cinfo mono">{c.ok ? (c.info || '—') : (c.detail || 'down')}</span>
          <span class="curl mono">{c.url.replace(/^https?:\/\//, '')}</span>
        </div>
      {/each}
      {#if components.length === 0}<div class="none label">NO COMPONENTS</div>{/if}
    </div>

    <div class="meta">
      {#if system?.model_ctx}
        <div class="mrow"><span class="label">CONTEXT</span><span class="mval mono">{(system.model_ctx / 1024).toFixed(0)}K tokens</span></div>
      {/if}
      {#if system?.typesense}
        <div class="mrow"><span class="label">MEMORY</span><span class="mval mono">{system.typesense.managed ? 'managed sidecar' : 'external'}</span></div>
      {/if}
    </div>

    <!-- Touch only: the hardware readout stacks below the stack status, so one
         tap on the orb shows everything instead of two popovers fighting over
         a phone-width bar. -->
    {#if stats}
      <div class="hw">
        <div class="phead"><span class="label">HARDWARE</span></div>
        <HardwareStats {stats} />
      </div>
    {/if}
  </div>
</div>

<style>
  .orbwrap { position: relative; display: flex; }

  .orb {
    display: inline-flex; align-items: center; justify-content: center;
    width: 30px; height: 30px;
    border: 1px solid var(--line2); border-radius: 50%;
    background: var(--s2); cursor: default; padding: 0;
  }
  .core { width: 8px; height: 8px; border-radius: 50%; background: var(--faint); transition: background .2s; }
  .orb.ok  { border-color: color-mix(in srgb, var(--ok) 40%, var(--line2)); }
  .orb.ok .core  { background: var(--ok); box-shadow: 0 0 0 3px color-mix(in srgb, var(--ok) 16%, transparent); }
  .orb.err { border-color: color-mix(in srgb, var(--err) 55%, var(--line2)); }
  .orb.err .core { background: var(--err); box-shadow: 0 0 0 3px color-mix(in srgb, var(--err) 20%, transparent); }

  .pop {
    position: absolute; top: calc(100% + 8px); right: 0;
    width: 340px; z-index: var(--z-popover);
    background: var(--surface); border-radius: 10px;
    box-shadow: var(--elev-2);
    opacity: 0; transform: translateY(-4px); pointer-events: none;
    transition: opacity .12s ease, transform .12s ease;
  }
  .orbwrap:hover .pop { opacity: 1; transform: none; pointer-events: auto; }

  /* Desktop keeps the two panels separate — there is room for both, and the
     hover target for hardware is the metric chips themselves. */
  .hw { display: none; }
  @media (max-width: 720px), (pointer: coarse) {
    .hw { display: block; border-top: 1px solid var(--line); }
  }

  /* Touch has no hover, so tapping the orb could never reveal this. The
     trigger is already a <button>, so focus-within is enough. */
  .orbwrap:focus-within .pop { opacity: 1; transform: none; pointer-events: auto; }

  /* The orb sits near the right edge, so a 340px panel anchored to it ran
     off the LEFT of a phone screen — component labels were sliced mid-word
     ("MODEL" showing as "L", "TYPESENSE" as "SENSE"). Pin it to the viewport
     instead of the trigger, sized to the room that actually exists. */
  @media (max-width: 720px), (pointer: coarse) {
    .pop {
      position: fixed;
      top: calc(var(--bar-h, 46px) + 6px);
      left: 8px; right: 8px;
      width: auto; max-width: 380px; margin-left: auto;
      max-height: calc(100dvh - var(--bar-h, 46px) - 24px);
      overflow-y: auto; overscroll-behavior: contain;
    }
  }

  .phead {
    display: flex; align-items: center; gap: 8px;
    padding: 11px 14px; border-bottom: 1px solid var(--line);
  }
  .pspace { flex: 1; }
  .tag.up { color: var(--dim); }

  .rows { padding: 8px; display: flex; flex-direction: column; }
  .row {
    display: grid; grid-template-columns: 8px 74px 1fr auto;
    align-items: center; gap: 10px;
    padding: 8px 6px; border-radius: var(--r);
  }
  .row + .row { border-top: 1px solid var(--line); }
  .dot { width: 7px; height: 7px; border-radius: 1px; background: var(--faint); }
  .dot.ok { background: var(--ok); } .dot.err { background: var(--err); }
  .cname { font-size: 11px; color: var(--text); text-transform: uppercase; letter-spacing: .05em; }
  .cinfo { font-size: 11px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .curl { font-size: 9px; color: var(--faint); text-align: right; }
  .none { padding: 14px; text-align: center; }

  .meta { border-top: 1px solid var(--line); padding: 10px 14px; display: flex; flex-direction: column; gap: 7px; }
  .mrow { display: flex; align-items: center; justify-content: space-between; }
  .mval { font-size: 11px; color: var(--dim); }
</style>
