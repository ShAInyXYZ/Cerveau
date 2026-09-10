<script lang="ts">
  import { Dot } from '../../kit/index.js';
  import { tooltip } from '../../kit/tooltip.js';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { ListChecks, ChevronRight, ChevronDown } from 'lucide-svelte';
  import { planCounts, planStatusLabel } from './planStatus';

  let planOpen = $state(true);
 const report=$derived(sessionStore.report);
 const counts=$derived(planCounts(report?.steps??[]));
 const finished=$derived(!!report&&report.steps.length>0&&report.done===report.steps.length);
  function stepTone(status: string): 'ok' | 'err' | 'warn' | 'off' {
    if (status === 'done') return 'ok';
    if (status === 'failed' || status === 'blocked') return 'err';
    if (['running','verifying','needs_reverify','unverified'].includes(status)) return 'warn';
    return 'off';
  }
</script>

{#if report}
  <div class="planwrap">
      <div class="planstrip" class:done={finished}>
        <button class="ps-head" onclick={() => (planOpen = !planOpen)}
          aria-expanded={planOpen}
          use:tooltip={planOpen ? 'collapse the plan' : 'show the plan steps'}>
          <ListChecks size={12} />
          <span class="ps-title">{report.title}</span>
          {#if report.handback}<span class="ps-chip warn">needs review</span>{/if}
          {#if planOpen}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
          <span class="ps-counts">
            {#each counts as c}<span class="c" class:ok={c.status==='done'} class:err={c.status==='blocked'} class:warn={c.status==='needs_reverify'}>{c.count} {c.label}</span>{/each}
          </span>
        </button>
        {#if planOpen}
          <ol class="ps-steps">
            {#each report.steps as s, i}
              <li class="ps-step" class:on={s.status === 'done'} class:bad={s.status === 'failed' || s.status === 'blocked'}
                class:part={s.status === 'needs_reverify'}>
                <Dot tone={stepTone(s.status)} size={5} />
                <span class="rnum tag">{String(i + 1).padStart(2, '0')}</span>
                <span class="ps-name">{s.title}</span>
                <span class="rstatus" use:tooltip={s.status==='needs_reverify'?'Previously passed; shared-file work requires a fresh check.':s.summary||planStatusLabel(s.status)}>{planStatusLabel(s.status)}</span>
              </li>
            {/each}
          </ol>
        {/if}
      </div>
  </div>
{/if}

<style>
  /* one plan row — the unit the 5-row window is measured in */
  .planwrap { --ps-row: 17px; }

  /* the strip mirrors the chat bar's inset (knob + gap), from shared tokens */
  .planwrap {
    align-self: stretch; margin: 0 var(--dock-inset) 8px;
    position: relative; z-index: var(--z-raised);
    overflow: hidden;
  }
  .planstrip {
    border-radius: 10px; overflow: hidden;
    background: var(--s1); box-shadow: inset 0 0 0 1px var(--line);
  }
  .planstrip.done { background: transparent; box-shadow: none; }

  .ps-head {
    display: flex; flex-wrap: wrap; align-items: center; gap: 8px; width: 100%;
    padding: 7px 11px; border: none; cursor: pointer;
    background: transparent; color: var(--dim); text-align: left;
  }
  .ps-head:hover { color: var(--text); }
  .ps-title {
    flex: 1; min-width: 0; font-size: 11.5px; font-weight: 600; color: var(--text);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .ps-counts { display: flex; flex-wrap: wrap; gap: 4px 10px; width: 100%; font-size: 11px; color: var(--muted); }
  .c.ok { color: var(--ok); } .c.err { color: var(--err); } .c.warn { color: var(--warn); }
  .rstatus { font-size: 10px; color: var(--muted); white-space: nowrap; }
  .ps-step.part .rstatus { color: var(--warn); }
  .ps-chip {
    font-size: 8.5px; letter-spacing: .1em; text-transform: uppercase;
    padding: 2px 6px; border-radius: 4px;
  }
  .ps-chip.warn { color: var(--warn); background: color-mix(in srgb, var(--warn) 14%, transparent); }
  .ps-steps {
    padding: 0 11px 8px; display: flex; flex-direction: column; gap: 3px;
    /* Show five rows and scroll the rest inside. A 23-step plan otherwise
       eats the whole screen and pushes the conversation out of view — and
       five is enough to see where you are without becoming the page. */
    max-height: calc(5 * var(--ps-row) + 8px);
    overflow-y: auto;
    overscroll-behavior: contain;   /* don't chain to the chat scroller */
  }
  .ps-step {
    display: flex; align-items: baseline; gap: 7px; font-size: 11px;
    min-height: var(--ps-row);
  }
  .rnum { color: var(--faint); }
  .ps-name {
    flex: 1; min-width: 0; color: var(--muted);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .ps-step.on .ps-name { color: var(--muted); }
  .ps-step.bad .ps-name { color: color-mix(in srgb, var(--err) 80%, var(--text)); }
  .ps-step.part .ps-name { color: var(--muted); }

  @media (max-width: 640px) {
    /* knobs stack below the bar on narrow screens; the strip goes full width */
    .planwrap { margin: 0 0 8px; --ps-row: 21px; }  /* taller touch rows */
  }
</style>
