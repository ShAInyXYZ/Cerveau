<script lang="ts">
  import { ChevronRight, ListTree } from 'lucide-svelte';
  import type { EpisodicEvent } from '../types';
  import { projectActivity } from './activity';
  import ActivityRows from './ActivityRows.svelte';

  // Preserve each original call and result inside a compact, inspectable log.
  let { events = [] }: { events?: EpisodicEvent[] } = $props();
  let open = $state(false);
  const items = $derived(projectActivity(events));
  const tools = $derived(items.filter(item => item.kind === 'tool').length);
  const notes = $derived(items.filter(item => item.kind !== 'tool' && item.kind !== 'result').length);
</script>

{#if items.length}
  <div class="turnlog">
    <button class="tlhead" onclick={() => (open = !open)} aria-expanded={open}
      aria-label={open ? 'hide what happened' : 'show what happened'}>
      <ListTree size={12} />
      <span>what happened</span>
      <span>{tools} tool call{tools === 1 ? '' : 's'}{notes ? ` · ${notes} note${notes === 1 ? '' : 's'}` : ''}</span>
      <ChevronRight class={open ? 'tchev open' : 'tchev'} size={12} />
    </button>
    {#if open}<ActivityRows {items} />{/if}
  </div>
{/if}

<style>
  .turnlog { margin: 0 0 8px; }
  .tlhead { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 7px; background: transparent; border: 0; border-radius: var(--r-lg); padding: 4px 0; cursor: pointer; color: var(--muted); font: inherit; font-size: 11px; }
  .tlhead:hover { color: var(--text); }
  .tlhead:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .tlhead :global(.tchev.open) { transform: rotate(90deg); }
</style>
