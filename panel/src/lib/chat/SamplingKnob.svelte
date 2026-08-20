<script lang="ts">
  import { tooltip } from '../../kit/tooltip.js';
  import { api } from '../api';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { Thermometer } from 'lucide-svelte';

  // A ONE-TURN override, not a second place to configure the default.
  //
  // The default lives in Settings and is what almost every turn uses. This is
  // for the message where you want something else — "give me three approaches"
  // wants Creative, "refactor this carefully" wants Strict — and it clears
  // itself afterwards so a deliberate choice never becomes the new normal.
  let sessionDefault = $state('strict');
  let presets = $state<string[]>([]);
  let open = $state(false);

  const TIP: Record<string, string> = {
    strict:   'temperature 0.2 — the measured default. Best for code.',
    neutral:  'temperature 0.55 — looser, for drafting.',
    creative: 'temperature 0.7 — widest spread, when there is no single right answer.'
  };

  $effect(() => {
    void api.getSampling().then((d) => {
      sessionDefault = d.active; presets = d.presets;
    }).catch(() => {});
  });

  const active = $derived(sessionStore.turnSampling || sessionDefault);
  const overridden = $derived(!!sessionStore.turnSampling);

  function pick(p: string) {
    sessionStore.turnSampling = p === sessionDefault ? '' : p;
    open = false;
  }
</script>

{#if presets.length}
  <div class="knob">
    <button class="pill" class:on={overridden} onclick={() => (open = !open)}
      aria-label="sampling for this message"
      use:tooltip={overridden
        ? `this message runs ${active} — the session default is ${sessionDefault}`
        : `sampling: ${sessionDefault}. Click to change it for this message only.`}>
      <Thermometer size={13} />
      {#if overridden}<span class="name">{active}</span>{/if}
    </button>

    {#if open}
      <div class="menu">
        {#each presets as p (p)}
          <button class="item" class:sel={p === active} onclick={() => pick(p)}
            use:tooltip={TIP[p] || p}>
            <span>{p}</span>
            {#if p === sessionDefault}<span class="def">default</span>{/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .knob { position: relative; display: inline-flex; }
  .pill {
    display: inline-flex; align-items: center; gap: 5px;
    background: none; border: 1px solid transparent; border-radius: 7px;
    padding: 5px 7px; cursor: pointer; color: var(--faint); font: inherit;
    transition: color .12s, border-color .12s;
  }
  .pill:hover { color: var(--text); }
  /* Only shows its colour when it is NOT the default — an always-lit control
     for a value you rarely change is noise in the bar. */
  .pill.on { color: var(--accent); border-color: var(--accent); }
  .name { font-size: 11px; text-transform: capitalize; }

  .menu {
    position: absolute; bottom: calc(100% + 7px); left: 0; z-index: 40;
    min-width: 148px; padding: 4px;
    background: var(--s2, #1B1B1E); border: 1px solid var(--line);
    border-radius: 9px; box-shadow: 0 10px 24px -10px rgba(0,0,0,.75);
  }
  .item {
    display: flex; align-items: center; justify-content: space-between; gap: 10px;
    width: 100%; padding: 7px 9px; border: 0; border-radius: 6px;
    background: none; color: var(--dim); font: inherit; font-size: 12.5px;
    cursor: pointer; text-transform: capitalize; text-align: left;
  }
  .item:hover { background: var(--panel); color: var(--text); }
  .item.sel { color: var(--accent); }
  .def {
    font-size: 9px; letter-spacing: .07em; text-transform: uppercase;
    color: var(--faint);
  }
</style>
