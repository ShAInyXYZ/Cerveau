<script lang="ts">
  // The chat view is a COMPOSITION — each concern is its own component and
  // reads the stores directly. This file only owns the scroll container.
  import Turns from './Turns.svelte';
  import ErrorCards from './ErrorCards.svelte';
  import QuestionCard from './QuestionCard.svelte';
  import WorkingLog from './WorkingLog.svelte';
  import Composer from './Composer.svelte';
  import { IconButton } from '../../kit';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { shouldFollow } from './autoscroll';

  let scroller = $state<HTMLElement | undefined>();
  let content = $state<HTMLElement | undefined>();

  // Follow the tail ONLY while already at it.
  //
  // This used to scroll unconditionally, so scrolling up to re-read something
  // during a long build meant the next message yanked you straight back down —
  // at exactly the moment you most want to look at earlier output.
  //
  // The decision is recorded ON SCROLL rather than measured inside the effect:
  // $effect runs after the DOM has already grown, so measuring there would ask
  // "are we at the bottom of the taller container", which is false the instant
  // anything arrives — and following would never resume.
  let following = $state(true);

  function onScroll() {
    if (scroller) following = shouldFollow(scroller);
  }

  function jumpToLatest() {
    following = true;
    if (scroller) scroller.scrollTop = scroller.scrollHeight;
  }

  $effect(() => {
    void sessionStore.activeId;
    following = true;
    const frame = requestAnimationFrame(jumpToLatest);
    return () => cancelAnimationFrame(frame);
  });

  // Observe content height, not message count: streaming text, tool output and
  // composer resizing can all move the tail without creating a new message.
  $effect(() => {
    if (!scroller || !content) return;
    let frame = 0;
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => { if (following) jumpToLatest(); });
    });
    observer.observe(content);
    observer.observe(scroller);
    return () => { observer.disconnect(); cancelAnimationFrame(frame); };
  });

</script>

<main class="chat">
  <!-- aria-live: the panel had none, so a streaming answer was either silent to
       a screen reader or re-read from the top on every token. atomic=false
       announces only what was added. -->
  <div class="stream-region">
   <div class="stream" bind:this={scroller} onscroll={onScroll}
    role="log" aria-label="Conversation" aria-live="polite" aria-atomic="false" aria-relevant="additions text">
   <div class="stream-content" bind:this={content}>
    <Turns />
    <ErrorCards />
    <QuestionCard />
    <WorkingLog />
   </div>
   </div>
   <div class="stream-fade" aria-hidden="true"></div>
   {#if !following}
    <div class="latest">
      <IconButton title="Jump to latest" onclick={jumpToLatest}>
        <svg class="latest-arrow" width="18" height="18" viewBox="0 0 20 20" aria-hidden="true">
          <path d="M10 4v12M5 11l5 5 5-5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </IconButton>
    </div>
   {/if}
  </div>
  <Composer />
</main>

<style>
  .chat { position: relative; flex: 1; display: flex; flex-direction: column; min-width: 0; min-height: 0; }
  .stream-region { position: relative; flex: 1; min-height: 0; min-width: 0; display: flex; width: 100%; max-width: 780px; margin: 0 auto; }
  .stream {
    /* Composer occupies its own layout row; no artificial overlay offset. */
    flex: 1; min-height: 0; overflow-y: auto; padding: 22px 26px 176px;
    overscroll-behavior: contain; scrollbar-gutter: stable;
    width: 100%; max-width: 780px; margin: 0 auto;
  }
  .stream-content { display: flex; flex-direction: column; gap: 20px; }
  .stream-content > :global(*) { flex-shrink: 0; }
  /* Fade only the scroll boundary; tail padding keeps the final content clear.
     This overlay never intercepts text selection, wheel or touch input. */
  .stream-fade { position: absolute; inset: auto 0 0; height: 160px; pointer-events: none; background: linear-gradient(to bottom, transparent 0%, var(--bg) 100%); }
  .latest { position: absolute; bottom: 20px; left: 50%; transform: translateX(-50%); z-index: var(--z-raised); }
  .latest :global(.ib) { display: grid; place-items: center; width: 44px; height: 44px; padding: 0; border: 0; border-radius: 50%; color: var(--text); background: color-mix(in srgb, var(--s2) 80%, transparent); backdrop-filter: blur(16px); box-shadow: inset 0 2px 5px rgb(0 0 0 / .65), inset 0 -1px 1px rgb(255 255 255 / .12); }
  .latest :global(.ib:hover:not(:disabled)) { background: color-mix(in srgb, var(--s3) 80%, transparent); }
  .latest :global(.ib:focus-visible) { outline: 2px solid var(--accent); outline-offset: 3px; }
  .latest-arrow { display: block; transform-origin: center; animation: latest-pulse calc(var(--t-slow) * 6) ease-in-out infinite; }
  @keyframes latest-pulse { 0%, 100% { transform: scale(1); } 50% { transform: scale(1.2); } }
  .latest:focus-within .latest-arrow { animation: none; }
  @media (hover: hover) and (pointer: fine) { .latest:hover .latest-arrow { animation-play-state: paused; } }
  @media (prefers-reduced-motion: reduce) { .latest-arrow { animation: none; } }

  @media (max-width: 640px) {
    .stream { padding: 12px 12px 176px; }
    .stream-content { gap: 14px; }
  }
</style>
