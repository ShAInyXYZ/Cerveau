<script lang="ts">
  import { sessionStore } from '../stores/session.svelte.ts';
  import { projectActivity } from './activity';
  import ActivityRows from './ActivityRows.svelte';

  const items = $derived(projectActivity(sessionStore.ticks.filter(event =>
    !sessionStore.run || event.payload?.run_id === sessionStore.run.id)));

  let now = $state(Date.now());
  $effect(() => {
    if (!sessionStore.running) return;
    const timer = setInterval(() => now = Date.now(), 500);
    return () => clearInterval(timer);
  });
  const elapsed = $derived(sessionStore.runStarted ? Math.max(0, Math.floor((now - sessionStore.runStarted) / 1000)) : 0);
</script>

{#if sessionStore.running}
  <section role="status" aria-live="polite" aria-atomic="false" class="working" aria-label="agent working log">
    <div class="whead">
      <span class="wname label">CERVEAU</span>
      <span class="wstatus">{(sessionStore.run?.status === 'running' ? sessionStore.run.phase : sessionStore.run?.status)?.replaceAll('_', ' ') || 'connecting'}</span>
      <span class="wtime tag">{elapsed}s</span>
    </div>
    {#if items.length}<ActivityRows {items} />{/if}
  </section>
{/if}

<style>
  .working { align-self: stretch; max-width: 780px; padding: 2px 0; min-width: 0; }
  .whead { display: flex; align-items: center; gap: 9px; }
  .wname { color: var(--dim); }
  .wstatus { font-family: var(--font-mono); font-size: 12px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .wtime { color: var(--muted); }
</style>
