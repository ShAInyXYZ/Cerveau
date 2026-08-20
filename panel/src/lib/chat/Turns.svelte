<script lang="ts">
  import Markdown from '../Markdown.svelte';
  import { fmtTime } from '../api';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { tooltip } from '../../kit/tooltip.js';
  import { Copy, Check, Pencil } from 'lucide-svelte';

  // Assistant turns that carry ONLY a tool call have empty text — their tool
  // call already shows in the working log, so an empty bubble is noise.
  const visible = $derived(
    sessionStore.messages.filter(
      (m) => m.type === 'msg.user' || (m.payload?.text ?? '').trim() !== '',
    ),
  );
  const empty = $derived(sessionStore.messages.length === 0 && !sessionStore.running);
  const isAuto = $derived(sessionStore.mode === 'autopilot');

  let copied = $state('');          // id of the message just copied
  let editing = $state('');         // id of the message being edited
  let draft = $state('');

  async function copy(id: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      copied = id;
      setTimeout(() => { if (copied === id) copied = ''; }, 1500);
    } catch { /* clipboard blocked — the text is on screen to select */ }
  }

  function startEdit(id: string, text: string) { editing = id; draft = text; }
  function cancelEdit() { editing = ''; draft = ''; }

  async function commitEdit(id: string) {
    const text = draft.trim();
    cancelEdit();
    await sessionStore.editAndResend(id, text);
  }
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
      {#if user && editing === (m.id ?? '')}
        <div class="edit">
          <textarea bind:value={draft} rows="3" aria-label="edit message"
            onkeydown={(e) => {
              if (e.key === 'Escape') cancelEdit();
              if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); commitEdit(m.id ?? ''); }
            }}></textarea>
          <div class="edit-row">
            <span class="edit-note">everything after this message is discarded</span>
            <button class="ebtn" onclick={cancelEdit}>Cancel</button>
            <button class="ebtn go" disabled={!draft.trim()} onclick={() => commitEdit(m.id ?? '')}>
              Send
            </button>
          </div>
        </div>
      {:else if user}
        <span class="utext">{m.payload?.text ?? ''}</span>
      {:else}
        <Markdown source={m.payload?.text ?? ''} />
      {/if}
    </div>

    {#if editing !== (m.id ?? '')}
      <div class="acts">
        <button class="act" onclick={() => copy(m.id ?? '', m.payload?.text ?? '')}
          use:tooltip={copied === (m.id ?? '') ? 'copied' : 'copy this message'}
          aria-label="copy message">
          {#if copied === (m.id ?? '')}<Check size={13} />{:else}<Copy size={13} />{/if}
        </button>
        {#if user && m.id && !sessionStore.running}
          <button class="act" onclick={() => startEdit(m.id ?? '', m.payload?.text ?? '')}
            use:tooltip={'edit and resend — discards everything after this message'}
            aria-label="edit message">
            <Pencil size={13} />
          </button>
        {/if}
      </div>
    {/if}
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

  /* Actions appear on hover. Always-visible buttons on every turn would put
     two icons beside every line of a long conversation — the actions matter
     rarely, the text matters always. Focus-within keeps them keyboard-reachable. */
  .acts {
    display: flex; gap: 2px; opacity: 0; transition: opacity .12s;
  }
  .turn.user .acts { flex-direction: row-reverse; }
  .turn:hover .acts, .turn:focus-within .acts { opacity: 1; }
  .act {
    display: inline-flex; align-items: center; justify-content: center;
    width: 24px; height: 24px; padding: 0; cursor: pointer;
    background: none; border: 0; border-radius: 5px; color: var(--faint);
  }
  .act:hover { background: var(--panel); color: var(--text); }

  /* ── inline edit ── */
  .edit { display: flex; flex-direction: column; gap: 8px; width: 100%; min-width: 320px; }
  .edit textarea {
    width: 100%; resize: vertical; font: inherit; font-size: 13.5px; line-height: 1.5;
    color: var(--text); background: var(--surface-raised);
    border: 1px solid var(--accent); border-radius: 10px; padding: 10px 12px;
  }
  .edit textarea:focus { outline: none; }
  .edit-row { display: flex; align-items: center; gap: 8px; justify-content: flex-end; }
  /* Say what the edit COSTS before it happens, not after. */
  .edit-note { flex: 1; font-size: 11px; color: var(--dim); text-align: left; }
  .ebtn {
    padding: 6px 14px; border-radius: 7px; cursor: pointer; font: inherit; font-size: 12.5px;
    background: none; border: 1px solid var(--line); color: var(--dim);
  }
  .ebtn:hover { color: var(--text); border-color: var(--dim); }
  .ebtn.go { background: var(--accent); border-color: var(--accent); color: #0B0B0D; font-weight: 600; }
  .ebtn.go:disabled { opacity: .5; cursor: not-allowed; }
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
