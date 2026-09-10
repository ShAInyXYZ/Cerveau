<script lang="ts">
  import { AlertTriangle, PauseCircle } from 'lucide-svelte';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { retryLabel } from './retryAction';
  import { incidentView } from './incidentView';
  const retry = $derived(retryLabel(sessionStore.run, sessionStore.plan));

  // Incident projection owns filtering/dismissal; keep its latest named event.
  const activeError = $derived.by(() => {
    const real = sessionStore.errors.filter((e) => {
      const what = (e.what ?? '').trim();
      if (!what) return false;
      return true;
    });
    return real.length ? real[real.length - 1] : null;
  });

  // The store targets the blocked plan step, or the last message without a plan.
  const lastUserText = $derived.by(() => {
    const ms = sessionStore.messages;
    for (let i = ms.length - 1; i >= 0; i--) {
      if (ms[i].type === 'msg.user') return ms[i].payload?.text ?? '';
    }
    return '';
  });
</script>

{#if activeError}
  {@const e = activeError}
  {@const view = incidentView(e, sessionStore.run, sessionStore.plan)}
  {#key e.id ?? e.what}
  <section class="incident" class:recoverable={view.recoverable} aria-label={view.title}>
    <div class="heading">
      {#if view.recoverable}<PauseCircle size={18} />{:else}<AlertTriangle size={18} />{/if}
      <h3>{view.title}</h3>
    </div>
    {#if view.subject}<p class="subject">{view.subject}</p>{/if}
    {#if view.explanation}<p>{view.explanation}</p>{/if}
    {#if view.diagnostic}<p class="diagnostic">Latest check: <code>{view.diagnostic}</code></p>{/if}
    <div class="actions">
      {#if sessionStore.run?.kind === 'reflex'}
        <span>Reflex: {sessionStore.run.reflex}. Review its result in the RFX panel before retrying.</span>
      {:else if lastUserText && retry}
        <button class="recover" disabled={sessionStore.running} onclick={() => sessionStore.retry(lastUserText)}>{retry}</button>
      {/if}
      <button class="dismiss" onclick={() => sessionStore.dismissAllErrors()}>Dismiss</button>
    </div>
    {#if view.detail}<details><summary>Technical details</summary><pre>{view.detail}</pre></details>{/if}
  </section>
  {/key}
{/if}

<style>
  .incident { flex-shrink:0; min-width:0; padding:var(--sp-7); background:var(--s1); border:1px solid var(--line); border-radius:var(--r-panel); color:var(--muted); }
  .heading { display:flex; align-items:center; gap:var(--sp-4); color:var(--text); }
  .heading :global(svg) { color:var(--err); flex-shrink:0; }
  .recoverable .heading :global(svg) { color:var(--warn); }
  h3 { font-size:var(--fs-title); font-weight:600; }
  p { margin-top:var(--sp-3); overflow-wrap:anywhere; }
  .subject { color:var(--text); }
  .actions { display:flex; flex-wrap:wrap; align-items:center; gap:var(--sp-4); margin-top:var(--sp-5); }
  button { min-height:36px; padding:var(--sp-4) var(--sp-6); border:1px solid var(--line2); border-radius:var(--r-control); background:var(--s2); color:var(--text); font:inherit; cursor:pointer; }
  .recover { border-color:var(--accent-line); background:var(--accent-soft); }
  button:hover:not(:disabled) { background:var(--s3); }
  button:disabled { opacity:.5; cursor:default; }
  .dismiss { border-color:transparent; background:transparent; color:var(--muted); }
  details { margin-top:var(--sp-5); }
  summary { width:fit-content; cursor:pointer; padding:var(--sp-2) 0; }
  pre { margin:var(--sp-4) 0 0; padding:var(--sp-5); background:var(--bg); font:var(--fs-small)/1.6 var(--font-mono); white-space:pre-wrap; overflow-wrap:anywhere; max-height:280px; overflow:auto; }
  @media(max-width:640px) { .incident { padding:var(--sp-5); } .recover { flex:1; } }
</style>
