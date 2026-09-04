<script lang="ts">
  import { tooltip } from '../../kit/tooltip.js';
  import { j, jpost } from '../api';

  // The thinking EFFORT, on the bar where the work is asked for. This is the
  // session setting (the same one as Settings → Thinking), not a one-turn
  // override: a build's effort is a decision about the build, and it should
  // be visible next to the prompt rather than four clicks away.
  //
  // "off" here means mode off; a level means the current mode (autopilot by
  // default) at that effort. Chat turns keep answering directly unless the
  // mode is "always" — that switch stays in Settings, it is rarely wanted.
  let mode = $state('plan');
  let effort = $state('medium');
  let levels = $state<string[]>([]);
  let open = $state(false);

  const TIP: Record<string, string> = {
    off:    'no reasoning — fastest',
    low:    'brief thinking, straight to the conclusion — the default for builds',
    medium: 'longer reasoning — 13k tokens on one planning step in testing; use for hard single steps',
    xhigh:  'the model\'s default: validate assumptions, weigh alternatives — thousands of tokens per call'
  };

  async function load() {
    try {
      const t = await j('/api/thinking');
      if (t?.efforts) { mode = t.mode; effort = t.effort; levels = ['off', ...t.efforts]; }
    } catch { /* older core */ }
  }
  $effect(() => { void load(); });

  const current = $derived(mode === 'off' ? 'off' : effort);

  async function pick(level: string) {
    open = false;
    const next = level === 'off'
      ? { mode: 'off', effort }
      : { mode: mode === 'off' ? 'plan' : mode, effort: level };
    mode = next.mode; effort = next.effort;              // optimistic: it is instant
    try { await jpost('/api/thinking', next); } catch { await load(); }
  }
</script>

{#if levels.length}
  <div class="knob">
    <button class="pill" onclick={() => (open = !open)}
      aria-label="thinking effort"
      use:tooltip={current === 'off'
        ? 'thinking: off. Click to let builds reason before each step.'
        : `thinking: ${effort}${mode === 'always' ? ' on every turn' : mode === 'autopilot' ? ' on every autopilot call' : ' while planning a build'}. Click to change.`}>
      <span class="label">THINK</span>
      <span class="name mono">{current}</span>
    </button>

    {#if open}
      <div class="menu">
        {#each levels as l (l)}
          <button class="item" class:sel={l === current} onclick={() => pick(l)} use:tooltip={TIP[l] || l}>
            <span>{l}</span>
            {#if l === 'low'}<span class="def">default</span>{/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  /* Sits opposite the workspace path and borrows its quietness: the same
     label-plus-mono pairing, a hairline pill, no icon, no colour unless the
     menu is open. The menu opens UPWARD like the sampling knob's, away from
     the input it belongs to. */
  .knob { position: relative; display: inline-flex; }
  .pill {
    display: inline-flex; align-items: center; gap: 7px;
    background: transparent; border: 1px solid var(--line2); border-radius: 999px;
    padding: 3px 10px 3px 9px; cursor: pointer; color: var(--faint); font: inherit;
    transition: color .1s, border-color .1s, background .1s;
  }
  .pill:hover { color: var(--muted); background: color-mix(in srgb, #fff 4%, transparent); }
  .label { font-size: 9px; letter-spacing: .1em; color: var(--faint); }
  .name { font-size: 11px; color: var(--dim); }
  .pill:hover .name { color: var(--muted); }

  .menu {
    position: absolute; bottom: calc(100% + 6px); left: 0; z-index: var(--z-dropdown);
    min-width: 132px; padding: 3px;
    background: var(--s2); border: 1px solid var(--line2); border-radius: 9px;
  }
  .item {
    display: flex; align-items: center; justify-content: space-between; gap: 10px;
    width: 100%; padding: 6px 9px; border: 0; border-radius: 6px;
    background: none; color: var(--dim); font: inherit; font-size: 12px;
    cursor: pointer; text-align: left;
  }
  .item:hover { background: var(--panel); color: var(--text); }
  .item.sel { color: var(--text); }
  .item.sel::after { content: ''; width: 5px; height: 5px; border-radius: 50%; background: var(--accent); }
  .def { font-size: 9px; letter-spacing: .07em; text-transform: uppercase; color: var(--faint); }
</style>
