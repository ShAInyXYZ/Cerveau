<script lang="ts">
  import { Notice } from '../../kit/index.js';
  import { sessionStore } from '../stores/session.svelte.ts';

  const q = $derived(sessionStore.question);
</script>

{#if q}
  <Notice
    tone="accent"
    kind="asks"
    title={q.question}
    choices={[
      ...(q.options ?? []).map((opt) => ({ label: opt, value: opt })),
      { label: 'Decide yourself', value: 'decide yourself', ghost: true },
    ]}
    onChoose={(v: string) => sessionStore.answer(v)}
  />
{/if}
