<script lang="ts">
  import { Button, Notice } from '../../kit/index.js';
  import { sessionStore } from '../stores/session.svelte.ts';

  // Show only the latest MEANINGFUL error: drop retry chatter and bare
  // cancellations — not failures the user can act on.
  const activeError = $derived.by(() => {
    const real = sessionStore.errors.filter((e) => {
      const what = (e.what ?? '').trim();
      const why = (e.why ?? e.detail ?? '').toLowerCase();
      if (!what) return false;
      if (/retrying/i.test(what)) return false;
      if (/context canceled|canceled|cancelled/.test(why)) return false;
      return true;
    });
    return real.length ? real[real.length - 1] : null;
  });

  // what "retry" re-runs: the last thing the user said
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
  <Notice
    tone="err"
    kind={e.class}
    title={e.what}
    detail={e.why}
    defaultOpen={true}
    meta={e.tried && e.tried.trim() && e.tried !== '·' ? [{ label: 'TRIED', value: e.tried }] : []}
  >
    {#snippet actions()}
      {#if lastUserText}
        <Button size="sm" variant="primary" onclick={() => sessionStore.retry(lastUserText)}>retry</Button>
      {/if}
      <Button size="sm" variant="ghost" onclick={() => sessionStore.dismissAllErrors()}>dismiss</Button>
    {/snippet}
  </Notice>
{/if}
