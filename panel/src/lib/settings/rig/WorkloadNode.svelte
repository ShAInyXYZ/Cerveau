<script lang="ts">
  // Something that runs: the Core, the embedder, Cerveau itself, Typesense,
  // or a stranger found holding memory on a card. The first two can be placed:
  // they carry a port to drag a link from.
  import { Handle, Position } from '@xyflow/svelte';
  import { tooltip } from '../../../kit/tooltip.js';
  import BrandMark from './BrandMark.svelte';
  import { rigLink } from './rigLink.svelte.ts';
  import type { WorkloadData } from './rigGraph';

  let { id, data }: { id: string; data: WorkloadData } = $props();
  const dragging = $derived(rigLink.from === data.role);
  const open = $derived(rigLink.selected === id);
</script>

<!-- a click opens the module's form; the diagram handles it (onnodeclick) -->
<div class="wl {data.role} {data.state}" class:linkable={data.linkable} class:dragging class:open style="--c: {data.color}">
  <div class="top">
    <BrandMark brand={data.brand} size={20} />
    <span class="title">{data.title}</span>
  </div>
  <!-- the state shares a line with the subtitle, never with the title: beside
       it, "vLLM · W8A16 · TP=4" was cut to "vLLM · W8A16 · T…" -->
  <div class="under">
    <span class="sub">{data.sub}</span>
    <span class="state mono"><i></i>{data.state}</span>
  </div>
  {#if data.chips.length}
    <div class="chips mono">{#each data.chips as c (c)}<span>{c}</span>{/each}</div>
  {/if}

  {#if data.linkable}
    <span class="grip" use:tooltip={data.role === 'core' ? 'drag to a card to run the Core on it' : 'drag to a card, or to the CPU'}>
      <Handle type="source" position={Position.Bottom} isConnectableEnd={false} class="rig-port" />
    </span>
  {:else}
    <!-- an anchor for its links; nothing to grab -->
    <Handle type="source" position={Position.Bottom} isConnectable={false} style="opacity:0" />
  {/if}
</div>

<style>
  .wl {
    --c: var(--dim);
    position: relative;
    width: 236px; box-sizing: border-box;
    background: var(--s2); border-radius: 9px;
    box-shadow: 0 0 0 1px var(--line2), 0 1px 0 0 var(--lift) inset;
    padding: 10px 12px 11px;
    border-top: 2px solid var(--c);
    cursor: pointer;
    transition: box-shadow var(--t-fast);
  }
  .wl:hover { box-shadow: 0 0 0 1px var(--ring-strong), 0 1px 0 0 var(--lift) inset; }
  /* its form is open: the ring ties the two together */
  .wl.open { box-shadow: 0 0 0 1.5px var(--c); }
  /* --c is the module's own colour, set from its brand: the same on its mark,
     its links and its share of a memory bar */
  .wl.parked, .wl.down, .wl.preview { background: var(--s1); }
  .wl.linkable { padding-bottom: 16px; }
  .wl.dragging { box-shadow: 0 0 0 1.5px var(--c); }

  .top { display: flex; align-items: center; gap: 7px; min-width: 0; }
  .title { flex: 1; min-width: 0; font-size: 13.5px; font-weight: 600; color: var(--text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .under { display: flex; align-items: baseline; gap: 8px; margin-top: 3px; }
  .state { flex-shrink: 0; display: inline-flex; align-items: center; gap: 5px; font-size: 10px; letter-spacing: .08em; text-transform: uppercase; color: var(--dim); }
  .state i { width: 6px; height: 6px; border-radius: 50%; background: var(--ok); }
  .parked .state i, .preview .state i { background: none; box-shadow: inset 0 0 0 1px var(--dim); }
  .down .state i { background: var(--err); }
  .down .state { color: var(--err); }

  .sub { flex: 1; min-width: 0; font-size: var(--fs-small); color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .chips { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 8px; font-size: 10px; color: var(--muted); }
  .chips span { padding: 2px 6px; border-radius: 3px; background: var(--s3); white-space: nowrap; }

  /* The port: a real handle, big enough to grab, in the module's own colour.
     It grows under the pointer so it reads as something to pull. */
  .grip { position: absolute; left: 50%; bottom: 0; width: 0; height: 0; }
  .wl :global(.rig-port) {
    width: 14px; height: 14px;
    border-radius: 50%; border: 2px solid var(--bg); background: var(--c);
    cursor: grab; transition: transform var(--t-fast);
  }
  .wl :global(.rig-port:hover), .wl.dragging :global(.rig-port) { transform: translate(-50%, 50%) scale(1.35); }
  .wl :global(.rig-port:active) { cursor: grabbing; }
</style>
