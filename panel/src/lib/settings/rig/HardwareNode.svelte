<script lang="ts">
  // A piece of the machine: its illustration, what it is, what is on it now —
  // and a place to drop a module.
  import { Handle, Position } from '@xyflow/svelte';
  import RigArt, { ART_WIDTH } from './art/RigArt.svelte';
  import { rigLink } from './rigLink.svelte.ts';
  import type { HardwareData } from './rigGraph';

  let { data }: { data: HardwareData } = $props();

  // While a link is being dragged: can it land here?
  const drop = $derived(rigLink.from === null ? '' : data.accepts.includes(rigLink.from) ? 'can' : 'cannot');
  const tone = $derived(drop === 'can' ? 'accent' : data.tone);
</script>

<div class="hw {data.tone} {drop}">
  <!-- The whole card is the drop target, not a 6px dot: the link is aimed at
       "that GPU". A link can end here but never start here. -->
  <Handle type="target" position={Position.Top} isConnectableStart={false}
    isConnectable={data.accepts.length > 0} class="rig-drop" />
  {#if data.accepts.length}<span class="port" aria-hidden="true"></span>{/if}

  <div class="art"><RigArt kind={data.kind} width={ART_WIDTH[data.kind]} load={data.load} {tone} /></div>
  <div class="head">
    <span class="title mono">{data.title}</span>
    <span class="name">{data.name}</span>
  </div>
  {#if data.facts}<div class="facts mono">{data.facts}</div>{/if}

  {#if data.total > 0}
    <div class="bar" role="img" aria-label={data.segments.map((s) => s.label).join(', ') || 'empty'}>
      {#each data.segments as s (s.role)}
        <i style="width:{Math.max(1.5, (s.mem / data.total) * 100)}%; background:{s.color}"></i>
      {/each}
    </div>
    <div class="legend mono">
      {#each data.segments as s (s.role)}<span><b style="background:{s.color}"></b>{s.label}</span>{/each}
      {#if !data.segments.length}<span class="free">empty</span>{/if}
    </div>
  {/if}

  {#if data.meters.length}
    <div class="meters mono">
      {#each data.meters as m (m.k)}<span><em>{m.k}</em> <b class={m.tone}>{m.v}</b></span>{/each}
    </div>
  {/if}
</div>

<style>
  .hw {
    position: relative;
    width: 236px; box-sizing: border-box;
    background: var(--s1); border-radius: 9px;
    box-shadow: 0 0 0 1px var(--line2);
    padding: 12px 14px;
    cursor: default;
    transition: opacity var(--t-fast), box-shadow var(--t-fast);
  }
  .hw.on { box-shadow: 0 0 0 1px var(--ring-strong), 0 1px 0 0 var(--lift) inset; }
  /* while a link is in the air */
  .hw.can { box-shadow: 0 0 0 1.5px var(--accent); background: color-mix(in srgb, var(--accent) 5%, var(--s1)); }
  .hw.cannot { opacity: .32; }

  /* the drop target: the card itself */
  .hw :global(.rig-drop) {
    top: 0; left: 0; width: 100%; height: 100%; transform: none;
    border: none; border-radius: 9px; background: transparent; opacity: 0;
  }
  /* where the links arrive */
  .port {
    position: absolute; top: -4px; left: 50%; width: 8px; height: 8px; margin-left: -4px;
    border-radius: 50%; background: var(--s1); box-shadow: 0 0 0 1.5px var(--dim);
    pointer-events: none; transition: box-shadow var(--t-fast), background var(--t-fast);
  }
  .can .port { background: var(--accent); box-shadow: 0 0 0 1.5px var(--accent); }

  .art { height: 118px; display: flex; align-items: center; justify-content: center; margin-bottom: 8px; pointer-events: none; }
  .head { display: flex; align-items: baseline; gap: 8px; min-width: 0; }
  .title { font-size: 10px; letter-spacing: .1em; text-transform: uppercase; color: var(--dim); flex-shrink: 0; }
  .name { font-size: 13.5px; font-weight: 600; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .facts { font-size: 10.5px; color: var(--dim); margin-top: 2px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

  .bar { display: flex; height: 7px; border-radius: 2px; overflow: hidden; background: var(--s3); margin: 10px 0 6px; }
  .bar i { display: block; height: 100%; }
  .legend { display: flex; flex-wrap: wrap; gap: 2px 10px; font-size: 10.5px; color: var(--muted); }
  .legend b { display: inline-block; width: 6px; height: 6px; border-radius: 1px; margin-right: 4px; }
  .legend .free { color: var(--faint); }

  .meters { display: flex; gap: 12px; margin-top: 9px; font-size: 10.5px; color: var(--dim); }
  .meters em { font-style: normal; letter-spacing: .08em; text-transform: uppercase; }
  .meters b { font-weight: 500; color: var(--text); font-variant-numeric: tabular-nums; }
  .meters b.warm { color: var(--warn); }
  .meters b.hot { color: var(--err); }
</style>
