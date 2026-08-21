<script lang="ts">
  import { Notice } from '../../kit/index.js';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { stripMarkdown } from '../markdown-safe';

  const q = $derived(sessionStore.question);
</script>

{#if q}
  <!-- Notice renders its title as plain text, so markdown the model wrote shows
       through as literal **asterisks** and backticks. Stripped here rather than
       instructing the model not to write it — that instruction never holds. -->
  <Notice
    tone="accent"
    kind="asks"
    title={stripMarkdown(q.question)}
    choices={[
      // value keeps the ORIGINAL text: the model matched on what it wrote,
      // and answering with a stripped variant could miss.
      ...(q.options ?? []).map((opt) => ({ label: stripMarkdown(opt), value: opt })),
      { label: 'Decide yourself', value: 'decide yourself', ghost: true },
    ]}
    onChoose={(v: string) => sessionStore.answer(v)}
  />
{/if}
