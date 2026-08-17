<script lang="ts">
  // The chat view is a COMPOSITION — each concern is its own component and
  // reads the stores directly. This file only owns the scroll container.
  import Turns from './Turns.svelte';
  import ErrorCards from './ErrorCards.svelte';
  import QuestionCard from './QuestionCard.svelte';
  import WorkingLog from './WorkingLog.svelte';
  import Composer from './Composer.svelte';
  import { sessionStore } from '../stores/session.svelte.ts';

  let scroller = $state<HTMLElement | undefined>();

  $effect(() => {
    void sessionStore.messages.length;
    if (scroller) queueMicrotask(() => (scroller!.scrollTop = scroller!.scrollHeight));
  });
</script>

<main class="chat">
  <div class="stream" bind:this={scroller}>
    <Turns />
    <ErrorCards />
    <QuestionCard />
    <WorkingLog />
  </div>
  <Composer />
</main>

<style>
  .chat { flex: 1; display: flex; flex-direction: column; min-width: 0; min-height: 0; }
  .stream {
    /* large bottom offset so the last message always scrolls clear of the
       floating dock and its gradient fade */
    flex: 1; overflow-y: auto; padding: 22px 26px 400px;
    display: flex; flex-direction: column; gap: 20px;
    width: 100%; max-width: 780px; margin: 0 auto;
  }
  /* stream children keep natural height — flex-column shrinks by default */
  .stream > :global(*) { flex-shrink: 0; }

  @media (max-width: 640px) {
    .stream { padding: 12px 12px 300px; gap: 14px; }
  }
</style>
