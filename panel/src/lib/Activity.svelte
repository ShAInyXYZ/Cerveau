<script>
  import { Segmented, Dot } from '../kit/index.js';
  import { tooltip } from '../kit/tooltip.js';
  import ActivityFlow from './ActivityFlow.svelte';
  import { relTime } from './api.js';
  import { X } from 'lucide-svelte';

  let { ticks = [], running = false, onClose } = $props();

  let view = $state('text');   // text | tree

  const toneFor = {
    'msg.user': 'accent', 'msg.assistant': 'ok',
    'tool.call': 'info', 'tool.result': 'info',
    'error': 'err', 'aborted': 'warn', 'interrupt': 'warn',
    'note': 'off', 'plan': 'semantic', 'turn.close': 'off', 'checkpoint': 'episodic'
  };

  function line(ev) {
    const p = ev.payload ?? {};
    const extra = p.name ?? p.text ?? p.kind ?? p.stop ?? p.status ?? '';
    return extra ? String(extra).slice(0, 48) : '';
  }
</script>

<aside class="act">
  <header class="ahead">
    <span class="label">ACTIVITY</span>
    {#if running}<Dot tone="accent" pulse size={5} />{/if}
    <div class="aspace"></div>
    <Segmented options={[{ value: 'text', label: 'Text' }, { value: 'tree', label: 'Tree' }]} bind:value={view} />
    <button class="xbtn" onclick={onClose} use:tooltip={"close"}><X size={13} /></button>
  </header>

  {#if view === 'text'}
    <div class="ticks">
      {#each [...ticks].reverse() as ev}
        <div class="tick">
          <Dot tone={toneFor[ev.type] ?? 'off'} size={5} />
          <span class="tid mono">{ev.id.slice(4)}</span>
          <span class="ttype mono">{ev.type.replace('msg.', '').replace('tool.', '')}</span>
          <span class="tx">{line(ev)}</span>
          <span class="tt mono">{relTime(ev.ts)}</span>
        </div>
      {/each}
      {#if ticks.length === 0}<div class="none label">NO ACTIVITY</div>{/if}
    </div>
  {:else}
    <div class="tree">
      <ActivityFlow {ticks} />
    </div>
  {/if}
</aside>

<style>
  .act {
    width: 340px; flex-shrink: 0;
    display: flex; flex-direction: column; min-height: 0;
    background: var(--s1); border-left: 1px solid var(--line);
  }
  .ahead {
    display: flex; align-items: center; gap: 8px;
    height: 34px; flex-shrink: 0; padding: 0 8px 0 12px;
    border-bottom: 1px solid var(--line);
  }
  .aspace { flex: 1; }
  .xbtn {
    display: inline-flex; align-items: center; justify-content: center;
    width: 24px; height: 24px; border: none; background: transparent; color: var(--dim); cursor: pointer;
  }
  .xbtn:hover { color: var(--text); }

  .ticks { flex: 1; overflow-y: auto; padding: 8px; display: flex; flex-direction: column; gap: 1px; }
  .tick {
    display: flex; align-items: center; gap: 8px;
    padding: 5px 8px; border-radius: 6px;
    font-size: 11px;
    transition: background .1s;
  }
  .tick:hover { background: color-mix(in srgb, #fff 3.5%, transparent); }
  .tid { color: var(--faint); width: 26px; flex-shrink: 0; }
  .ttype { color: var(--muted); width: 62px; flex-shrink: 0; overflow: hidden; text-overflow: ellipsis; }
  .tx { flex: 1; color: var(--dim); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tt { color: var(--faint); flex-shrink: 0; font-size: 9px; }
  .none { padding: 20px; text-align: center; }

  .tree { flex: 1; min-height: 0; }
</style>
