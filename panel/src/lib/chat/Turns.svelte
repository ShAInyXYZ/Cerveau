<script lang="ts">
  import Markdown from '../Markdown.svelte';
  import { fmtTime } from '../api';
  import { sessionStore } from '../stores/session.svelte.ts';

  // Assistant turns that carry ONLY a tool call have empty text — their tool
  // call already shows in the working log, so an empty bubble is noise.
  const visible = $derived(
    sessionStore.messages.filter(
      (m) => m.type === 'msg.user' || (m.payload?.text ?? '').trim() !== '',
    ),
  );
  const empty = $derived(sessionStore.messages.length === 0 && !sessionStore.running);
  const isAuto = $derived(sessionStore.mode === 'autopilot');
</script>

{#if empty}
  <div class="void">
    <div class="void-mark">CERVEAU</div>
    <div class="label">
      {isAuto ? 'AUTOPILOT · FULL AUTONOMY — DESCRIBE THE TASK' : 'READY · TYPE TO BEGIN'}
    </div>
  </div>
{/if}

{#each visible as m, i (m.id ?? m.ts ?? i)}
  {@const user = m.type === 'msg.user'}
  <article class="turn" class:user>
    <div class="tmeta">
      <span class="label">{user ? 'YOU' : 'CERVEAU'}</span>
      <span class="tag">{fmtTime(m.ts)}</span>
    </div>
    <div class="tbody">
      {#if user}
        <span class="utext">{m.payload?.text ?? ''}</span>
      {:else}
        <Markdown source={m.payload?.text ?? ''} />
      {/if}
    </div>
  </article>
{/each}

<style>
  .void { margin: auto; text-align: center; display: flex; flex-direction: column; gap: 12px; align-items: center; }
  .void-mark { font-family: var(--font-mono); letter-spacing: .5em; font-size: 13px; color: var(--faint); }

  .turn {
    display: flex; flex-direction: column; gap: 6px; max-width: var(--composer-w);
    /* long sessions render hundreds of turns — let the browser skip
       offscreen ones entirely */
    content-visibility: auto;
    contain-intrinsic-size: auto 80px;
  }
  .turn.user { align-self: flex-end; align-items: flex-end; max-width: 620px; }
  .tmeta { display: flex; align-items: baseline; gap: 8px; }
  .turn.user .tmeta { flex-direction: row-reverse; }
  .utext {
    display: inline-block;
    background: var(--surface-raised); border-radius: 10px;
    box-shadow: var(--elev-1);
    padding: 11px 14px; font-size: 13.5px; line-height: 1.55; white-space: pre-wrap;
    color: var(--text);
  }
  @media (max-width: 640px) {
    .turn.user { max-width: 85%; }
  }
</style>
