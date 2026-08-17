<script lang="ts">
  import { Dot } from '../../kit/index.js';
  import { tooltip } from '../../kit/tooltip.js';
  import { storage, storageKeys } from '../storage';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { ListChecks, Check, ChevronRight, ChevronDown } from 'lucide-svelte';

  const CELEBRATE_MS = 2400;
  const COLLAPSE_MS = 520;

  let planOpen = $state(true);
  const report = $derived(sessionStore.report);

  // ── plan completion: celebrate briefly, then archive the strip ──
  // The report stays in the session log; a per-plan flag hides the strip.
  type Phase = 'live' | 'celebrate' | 'collapsing' | 'archived';
  let planPhase = $state<Phase>('live');
  let celebrated = false; // plain guard, NOT reactive — phase writes can't retrigger
  const planId = $derived(report?.plan_event_id ?? report?.title ?? '');
  const planComplete = $derived(
    !!report && report.steps.length > 0 && report.steps.every((s) => s.status === 'done'),
  );
  const reduceMotion =
    typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches;

  // The timer sequence runs OUTSIDE the effect: report polls constantly, and an
  // effect owning timers would re-run and cancel them (learned the hard way).
  function runCompletionSequence(): void {
    const stamp = () => storage.set(storageKeys.planArchived(planId), '1');
    if (reduceMotion) { planPhase = 'archived'; stamp(); return; }
    planPhase = 'celebrate';
    setTimeout(() => (planPhase = 'collapsing'), CELEBRATE_MS);
    setTimeout(() => { planPhase = 'archived'; stamp(); }, CELEBRATE_MS + COLLAPSE_MS);
  }

  $effect(() => {
    if (!report) { planPhase = 'live'; celebrated = false; return; }
    if (planId && storage.get<string>(storageKeys.planArchived(planId), '') === '1') {
      planPhase = 'archived';
      return;
    }
    if (planComplete && !celebrated) {
      celebrated = true;
      runCompletionSequence();
    }
  });

  const pending = $derived(
    report ? report.steps.filter((s) => s.status !== 'done' && s.status !== 'failed').length : 0,
  );
  const finished = $derived(pending === 0);

  function stepTone(status: string): 'ok' | 'err' | 'warn' | 'off' {
    if (status === 'done') return 'ok';
    if (status === 'failed') return 'err';
    if (status === 'partial') return 'warn';
    return 'off';
  }
</script>

{#if report && planPhase !== 'archived'}
  <div class="planwrap" class:collapsing={planPhase === 'collapsing'}>
    {#if planPhase === 'celebrate'}
      <div class="planstrip done celebrate" role="status">
        <div class="ps-done">
          <span class="ps-check"><Check size={13} strokeWidth={3} /></span>
          <span class="ps-done-text">Plan complete — all {report.steps.length} steps done</span>
        </div>
      </div>
    {:else}
      <div class="planstrip" class:done={finished}>
        <button class="ps-head" onclick={() => (planOpen = !planOpen)}
          aria-expanded={planOpen}
          use:tooltip={planOpen ? 'collapse the plan' : 'show the plan steps'}>
          <ListChecks size={12} />
          <span class="ps-title">{report.title}</span>
          <span class="ps-counts mono">
            <span class="c ok">{report.done}</span>
            {#if report.failed}<span class="c err">{report.failed}</span>{/if}
            {#if pending}<span class="c dim">{pending}</span>{/if}
          </span>
          {#if report.handback}<span class="ps-chip warn">handback</span>{/if}
          {#if planOpen}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
        </button>
        {#if planOpen}
          <ol class="ps-steps">
            {#each report.steps as s, i}
              <li class="ps-step" class:on={s.status === 'done'} class:bad={s.status === 'failed'}
                class:part={s.status === 'partial'}>
                <Dot tone={stepTone(s.status)} size={5} />
                <span class="rnum tag">{String(i + 1).padStart(2, '0')}</span>
                <span class="ps-name">{s.title}</span>
                <span class="rstatus label">{s.status}</span>
              </li>
            {/each}
          </ol>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  /* the strip mirrors the chat bar's inset (knob + gap), from shared tokens */
  .planwrap {
    align-self: stretch; margin: 0 var(--dock-inset) 8px;
    position: relative; z-index: var(--z-raised);
    overflow: hidden;
  }
  .planwrap.collapsing {
    animation: plan-collapse .5s var(--ease-mech) forwards;
  }
  @keyframes plan-collapse {
    from { max-height: 200px; opacity: 1; transform: none; margin-bottom: 8px; }
    60%  { opacity: 0; }
    to   { max-height: 0; opacity: 0; transform: translateY(-4px); margin-bottom: 0; }
  }
  .planstrip {
    border-radius: 10px; overflow: hidden;
    background: var(--s1); box-shadow: inset 0 0 0 1px var(--line);
  }
  .planstrip.done { opacity: .72; }

  .planstrip.celebrate {
    opacity: 1;
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--ok) 45%, transparent);
    animation: plan-glow 1.6s ease-out;
  }
  @keyframes plan-glow {
    0%   { box-shadow: inset 0 0 0 1px var(--line); }
    25%  { box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--ok) 70%, transparent),
                       0 0 0 3px color-mix(in srgb, var(--ok) 16%, transparent); }
    100% { box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--ok) 45%, transparent); }
  }
  .ps-done { display: flex; align-items: center; gap: 9px; padding: 9px 12px; }
  .ps-check {
    display: inline-flex; align-items: center; justify-content: center;
    width: 20px; height: 20px; border-radius: 50%; flex-shrink: 0;
    background: var(--ok); color: var(--bg);
    animation: plan-pop .4s cubic-bezier(.2, 1.4, .4, 1) both;
  }
  @keyframes plan-pop { from { transform: scale(0); } to { transform: scale(1); } }
  .ps-done-text { font-size: 12px; font-weight: 600; color: var(--ok); }

  .ps-head {
    display: flex; align-items: center; gap: 8px; width: 100%;
    padding: 7px 11px; border: none; cursor: pointer;
    background: transparent; color: var(--dim); text-align: left;
  }
  .ps-head:hover { color: var(--text); }
  .ps-title {
    flex: 1; min-width: 0; font-size: 11.5px; font-weight: 600; color: var(--text);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .ps-counts { display: flex; gap: 6px; font-size: 10px; }
  .c.ok { color: var(--ok); } .c.err { color: var(--err); } .c.dim { color: var(--dim); }
  .ps-chip {
    font-size: 8.5px; letter-spacing: .1em; text-transform: uppercase;
    padding: 2px 6px; border-radius: 4px;
  }
  .ps-chip.warn { color: var(--warn); background: color-mix(in srgb, var(--warn) 14%, transparent); }
  .ps-steps { padding: 0 11px 8px; display: flex; flex-direction: column; gap: 3px; }
  .ps-step { display: flex; align-items: baseline; gap: 7px; font-size: 11px; }
  .rnum { color: var(--faint); }
  .ps-name {
    flex: 1; min-width: 0; color: var(--muted);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .ps-step.on .ps-name { color: var(--text); }
  .ps-step.bad .ps-name { color: color-mix(in srgb, var(--err) 80%, var(--text)); }
  .ps-step.part .ps-name { color: var(--warn); }

  @media (max-width: 640px) {
    /* knobs stack below the bar on narrow screens; the strip goes full width */
    .planwrap { margin: 0 0 8px; }
  }
</style>
