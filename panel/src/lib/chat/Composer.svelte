<script lang="ts">
  import ModeKnob from '../ModeKnob.svelte';
  import AttachKnob from '../AttachKnob.svelte';
  import WorkspacePath from '../WorkspacePath.svelte';
  import PlanStrip from './PlanStrip.svelte';
  import { tooltip } from '../../kit/tooltip.js';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { healthStore } from '../stores/health.svelte.ts';
  import { ArrowUp } from 'lucide-svelte';
  import type { Mode } from '../types';

  let draft = $state('');
  const running = $derived(sessionStore.running);
  const isAuto = $derived(sessionStore.mode === 'autopilot');

  // ModeKnob still binds a plain value — bridge it to the store
  let mode = $state<Mode>(sessionStore.mode);
  $effect(() => { sessionStore.mode = mode; });
  $effect(() => { mode = sessionStore.mode; });

  function submit(): void {
    const t = draft.trim();
    if (!t) return;
    draft = '';
    if (running) void sessionStore.steer(t);
    else void sessionStore.send(t);
  }

  const placeholder = $derived(
    running ? 'steer the running turn…'
    : isAuto ? 'describe the task — autopilot runs it end to end…'
    : 'message cerveau…',
  );
</script>

<div class="dockzone">
  <div class="dockstack">
    {#if !sessionStore.activeIsInstant}
      <div class="wsline">
        <WorkspacePath workspace={healthStore.workspace}
          onChanged={(ws: string) => sessionStore.onWorkspaceChanged(ws)} />
      </div>
    {/if}

    <PlanStrip />

    <div class="dockrow">
      <ModeKnob bind:mode />

      <div class="bar">
        <textarea
          class="input"
          bind:value={draft}
          rows="1"
          aria-label="message input"
          {placeholder}
          onkeydown={(e) => e.key === 'Enter' && !e.shiftKey && (e.preventDefault(), submit())}
        ></textarea>
        {#if running}<span class="steerbadge label">STEER</span>{/if}
        <button class="send" class:steer={running} disabled={!draft.trim()} onclick={submit}
          aria-label={running ? 'steer the running turn' : 'send message'}
          use:tooltip={running ? 'steer' : 'send'}>
          <ArrowUp size={17} strokeWidth={2.5} />
        </button>
      </div>

      <AttachKnob modalities={healthStore.value?.model?.modalities ?? null}
        onAttach={() => { /* attach flow not built yet */ }} />
    </div>
  </div>
</div>

<style>
  .dockzone {
    flex-shrink: 0;
    padding: 0 26px 20px;
    display: flex; justify-content: center;
    background: linear-gradient(to top, var(--bg) 60%, transparent);
  }
  /* the plan strip stacks ABOVE the composer; both share one width so they
     read as a single unit */
  .dockstack { width: 100%; max-width: var(--composer-w); display: flex; flex-direction: column; }
  .wsline {
    align-self: flex-end; margin: 0 var(--dock-inset) 6px 0;
    position: relative; z-index: var(--z-raised);
  }
  .dockrow { width: 100%; display: flex; align-items: center; gap: var(--dock-gap); }
  .dockrow > :global(.knobbtn) { align-self: center; }

  .bar {
    position: relative;
    flex: 1; min-width: 0;
    display: flex; align-items: center; gap: 8px;
    background: var(--surface-raised);
    border-radius: 999px;
    padding: 6px 6px 6px 18px;
    box-shadow: var(--elev-2);
    transition: box-shadow var(--t-fast);
  }
  .bar:focus-within {
    box-shadow:
      0 0 0 1px var(--accent-line),
      0 1px 0 0 var(--lift-strong) inset,
      0 -1px 0 0 var(--shade) inset;
  }

  .input {
    flex: 1; min-width: 0;
    background: transparent; border: none; outline: none; resize: none;
    color: var(--text); font-family: var(--font-sans);
    font-size: 14px; line-height: 1.5;
    padding: 8px 0;
    max-height: 120px;
  }
  .input::placeholder { color: var(--faint); }

  .steerbadge { color: var(--accent); letter-spacing: .18em; flex-shrink: 0; }

  .send {
    display: inline-flex; align-items: center; justify-content: center;
    width: 42px; height: 42px; flex-shrink: 0;
    border-radius: 50%;
    border: 1px solid var(--accent);
    background: var(--accent); color: var(--accent-ink);
    cursor: pointer;
    transition: filter var(--t-fast), transform 50ms, background var(--t-fast);
  }
  .send:hover:not(:disabled) { filter: brightness(1.08); }
  .send:active:not(:disabled) { transform: scale(.94); }
  .send:disabled { opacity: .3; cursor: default; background: var(--s3); border-color: var(--line2); color: var(--dim); }
  .send.steer { background: var(--s3); color: var(--accent); border-color: var(--accent-line); }

  @media (max-width: 640px) {
    .dockzone { padding: 0 10px 10px; }
    /* input gets the full width; the ws chip aligns to the edge */
    .wsline { margin-right: 0; }
  }
</style>
