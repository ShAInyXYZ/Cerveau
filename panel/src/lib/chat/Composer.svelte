<script lang="ts">
  import { untrack } from 'svelte';
  import CaptureDialog from './CaptureDialog.svelte';
  import type { ImageAttachment } from './images';
  import { jpost } from '../api';
  import ModeKnob from '../ModeKnob.svelte';
  import AttachKnob from '../AttachKnob.svelte';
  import WorkspacePath from '../WorkspacePath.svelte';
  import PlanStrip from './PlanStrip.svelte';
 import RunStatus from './RunStatus.svelte';
  import SamplingKnob from './SamplingKnob.svelte';
  import ThinkingKnob from './ThinkingKnob.svelte';
  import { tooltip } from '../../kit/tooltip.js';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { healthStore } from '../stores/health.svelte.ts';
  import { ArrowUp } from 'lucide-svelte';
  import type { Mode } from '../types';

  let draft = $state('');
  let input: HTMLTextAreaElement;
  let multiline = $state(false);
  let images = $state<ImageAttachment[]>([]);
  let attachOpen = $state(false), attachError = $state('');
  const drafts = new Map<string, { text: string; images: ImageAttachment[] }>();
  let draftSession = '';
  const vision = $derived(healthStore.value?.model?.modalities?.vision);

  $effect(() => {
    const sid = sessionStore.activeId ?? '';
    untrack(() => {
      if (draftSession) drafts.set(draftSession, { text: draft, images: [...images] });
      if (drafts.size > 20) drafts.delete(drafts.keys().next().value!);
      draftSession = sid;
      const saved = drafts.get(sid);
      draft = saved?.text ?? ''; images = saved?.images ?? [];
      attachOpen = false; attachError = '';
    });
  });
  $effect(() => {
    void draft;
    if (!draft) multiline = false;
    resizeInput();
  });
  function resizeInput() {
    if (input) {
      input.style.height = 'auto';
      input.style.height = `${Math.min(input.scrollHeight, 160)}px`;
      // Once expanded, keep its stable shape until the draft is cleared.
      // Otherwise the wider multiline text area can repeatedly collapse itself.
      if (draft && (input.scrollHeight > 52 || draft.includes('\n'))) multiline = true;
    }
  }
  $effect(() => {
    if (!input) return;
    let width = -1;
    let frame = 0;
    const observer = new ResizeObserver(entries => {
      const next = entries[0]?.contentRect.width;
      if (next !== undefined && next !== width) {
        width = next;
        cancelAnimationFrame(frame);
        frame = requestAnimationFrame(resizeInput);
      }
    });
    observer.observe(input);
    return () => { observer.disconnect(); cancelAnimationFrame(frame); };
  });
  function addImage(image: ImageAttachment) {
    if (images.length >= 2) { attachError = 'Attach at most two images per message.'; return; }
    images = [...images, image]; attachError = '';
  }
  $effect(() => {
    async function fromReflex(event: Event) {
      const detail = (event as CustomEvent<{ sessionId: string; path: string; sha256: string }>).detail;
      const sid = sessionStore.activeId;
      if (!detail || detail.sessionId !== sid) return;
      if (images.length >= 2) { attachError = 'Attach at most two images per message.'; return; }
      try {
        if (!sid) return;
        const result = await jpost<{image: ImageAttachment}>(`/api/sessions/${encodeURIComponent(sid)}/images/devcheck`, { path: detail.path, sha256: detail.sha256 });
        if (sid === sessionStore.activeId) addImage({ ...result.image, name: 'DevCheck capture' });
      } catch (e) { if (sid === sessionStore.activeId) attachError = String(e); }
    }
    window.addEventListener('rfx:attach-image', fromReflex);
    return () => window.removeEventListener('rfx:attach-image', fromReflex);
  });
  const running = $derived(sessionStore.running);
  const isAuto = $derived(sessionStore.mode === 'autopilot');

  // ModeKnob still binds a plain value — bridge it to the store
  let mode = $state<Mode>(sessionStore.mode);
  $effect(() => { sessionStore.mode = mode; });
  $effect(() => { mode = sessionStore.mode; });

  async function submit(): Promise<void> {
    const t = draft.trim();
    if ((!t && !images.length) || sessionStore.submitting) return;
    if (images.length && (running || vision === false)) return;
    const sid=sessionStore.activeId;
    const attached = images;
    const ok=running?await sessionStore.steer(t):await sessionStore.send(t, { images: attached });
    if(ok && sid===sessionStore.activeId && draft.trim()===t) { draft=''; if(images===attached)images=[]; }
  }

  const placeholder = $derived(
    running ? 'steer the running turn…'
    : isAuto ? 'describe the task — autopilot runs it end to end…'
    : 'message cerveau…',
  );
</script>

<div class="dockzone">
  <div class="dockstack">
    <!-- the line above the bar: thinking effort on the left, the workspace
         on the right — the two facts about HOW the next prompt will run. -->
    <div class="wsline">
      <span class="wsleft"><ThinkingKnob /></span>
      {#if !sessionStore.activeIsInstant}
        <WorkspacePath workspace={sessionStore.workspace}
          onChanged={(ws: string) => sessionStore.onWorkspaceChanged(ws)} />
      {/if}
    </div>

    <RunStatus />
    <PlanStrip />

    {#if images.length}
      <div class="attachments" aria-label="Images attached to next message">
        {#each images as image, i}
          <div class="attachment"><img src={image.data_url} alt={image.name || 'Attached image'} /><div><span>{image.name || 'Image'}</span><small>{image.width} × {image.height} · {Math.ceil((image.bytes ?? 0)/1024)} KiB</small></div><button onclick={() => images = images.filter((_, index) => index !== i)} aria-label={`Remove ${image.name || 'image'}`}>Remove</button></div>
        {/each}
      </div>
      {#if running}<p class="attach-note">Images are kept for your next message. Steering accepts text only.</p>
      {:else if vision === false}<p class="attach-note" role="status">The Core reports no image support. Remove images to send text.</p>
      {:else if vision !== true}<p class="attach-note">Core image support is unconfirmed. This message will include image content.</p>{/if}
    {/if}
    {#if attachError}<p class="attach-error" role="alert">{attachError}</p>{/if}

    <div class="dockrow">
      <ModeKnob bind:mode />

      <div class="bar" class:multiline>
        <textarea
          class="input"
          bind:value={draft}
          bind:this={input}
          rows="1"
          aria-label="message input"
          {placeholder}
          onkeydown={(e) => e.key === 'Enter' && !e.shiftKey && !e.isComposing && (e.preventDefault(), submit())}
        ></textarea>
        <div class="input-actions">
        <SamplingKnob />
        {#if running}<span class="steerbadge">Steer</span>{/if}
        <button class="send" class:steer={running} disabled={(!draft.trim() && !images.length) || sessionStore.submitting || (!!images.length && (running || vision === false))} onclick={submit}
          aria-label={running ? 'steer the running turn' : 'send message'}
          use:tooltip={running ? 'steer' : 'send'}>
          <ArrowUp size={17} strokeWidth={2.5} />
        </button>
        </div>
      </div>

      <AttachKnob modalities={healthStore.value?.model?.modalities ?? null}
        onAttach={() => { attachOpen = true; }} />
    </div>
  </div>
</div>
{#if attachOpen}<CaptureDialog onAttach={addImage} onClose={() => attachOpen = false} />{/if}

<style>
  .dockzone {
    flex-shrink: 0;
    padding: 12px 26px 20px;
    display: flex; justify-content: center;
    background: var(--bg);
  }
  /* the plan strip stacks ABOVE the composer; both share one width so they
     read as a single unit */
  .dockstack { width: 100%; max-width: var(--composer-w); display: flex; flex-direction: column; }
  .wsline {
    align-self: stretch; margin: 0 var(--dock-inset) 6px var(--dock-inset);
    position: relative; z-index: var(--z-raised);; display: flex; align-items: center; justify-content: space-between; }
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
  .bar.multiline { flex-direction:column; align-items:stretch; border-radius:var(--sp-6); padding:var(--sp-5); }
  .input-actions { display:flex; align-items:center; gap:var(--sp-4); flex-shrink:0; }
  .multiline .input-actions { align-self:flex-end; }
  .multiline .input { flex:none; width:100%; min-height:24px; padding:0 var(--sp-2); }

  .input {
    flex: 1; min-width: 0;
    background: transparent; border: none; outline: none; resize: none;
    color: var(--text); font-family: var(--font-sans);
    font-size: 14px; line-height: 1.5;
    padding: 8px 0;
    max-height: 160px;
    scrollbar-gutter:stable;
  }
  .input::placeholder { color: var(--muted); }
  .attachments { display:flex; flex-wrap:wrap; gap:8px; margin:0 var(--dock-inset) 8px; }
  .attachment { display:flex; align-items:center; gap:8px; max-width:100%; background:var(--s2); border:1px solid var(--line2); border-radius:8px; padding:6px; font-size:12px; }
  .attachment img { width:48px; height:40px; object-fit:contain; } .attachment span { overflow-wrap:anywhere; } .attachment small { display:block; color:var(--muted); } .attachment button { margin-left:auto; border:0; background:transparent; color:var(--muted); font:inherit; cursor:pointer; padding:6px; }
  .attach-note,.attach-error { margin:0 var(--dock-inset) 8px; color:var(--muted); font-size:12px; } .attach-error { color:var(--err); }

  .steerbadge { color: var(--muted); font-size:var(--fs-small); flex-shrink: 0; }

  .send {
    display: inline-flex; align-items: center; justify-content: center;
    width: 42px; height: 42px; flex-shrink: 0;
    border-radius: 50%;
    border: 1px solid var(--accent);
    background: var(--accent); color: var(--on-accent);
    cursor: pointer;
    transition: filter var(--t-fast), transform 50ms, background var(--t-fast);
  }
  .send:hover:not(:disabled) { filter: brightness(1.08); }
  .send:active:not(:disabled) { transform: scale(.94); }
  .send:disabled { opacity: .3; cursor: default; background: var(--s3); border-color: var(--line2); color: var(--dim); }
  .send.steer { background: var(--s3); color: var(--accent); border-color: var(--accent-line); }

  @media (max-width: 640px) {
    .dockzone { padding: 10px; }
    .dockrow { flex-wrap:wrap; }
    .bar { order:-1; flex-basis:100%; }
    .dockrow > :global(.attach) { margin-left:auto; }
    /* input gets the full width; the ws chip aligns to the edge */
    .wsline { margin-right: 0; flex-wrap: wrap; gap: 6px; }
  }
  .wsleft { margin-right: auto; }
</style>
