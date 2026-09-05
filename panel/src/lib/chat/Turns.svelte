<script lang="ts">
  import Markdown from '../Markdown.svelte';
  import TurnLog from './TurnLog.svelte';
  import { fmtTime } from '../api';
  import { sessionStore } from '../stores/session.svelte.ts';
  import { tooltip } from '../../kit/tooltip.js';
  import { splitStreaming } from '../markdown-safe';
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

  // null while nothing is copied — an optimistic echo has no id, and comparing
  // '' to '' would tick every id-less message at once.
  let copied = $state<string | null>(null);
  // null, not '': an optimistic echo has no id yet, and `editing === (m.id ?? '')`
  // made every freshly-sent message match the "nothing is being edited" state
  // and render as an edit box.
  let editing = $state<string | null>(null);
  let draft = $state('');

  async function copy(key: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      copied = key;
      setTimeout(() => { if (copied === key) copied = null; }, 1500);
    } catch { /* clipboard blocked — the text is on screen to select */ }
  }

  function startEdit(id: string, text: string) { editing = id; draft = text; }

  /**
   * How many tool calls happened after this message.
   *
   * Rewinding removes them from the CONVERSATION but never from the disk —
   * deleting files would be far worse (the model may have edited files you also
   * touched, and "undo three writes" is not reliably invertible from a log).
   * The honest thing is to say so, and only when it actually applies.
   */
  function toolsAfter(id: string): number {
    const msgs = sessionStore.messages;
    const i = msgs.findIndex((m) => m.id === id);
    if (i < 0) return 0;
    return msgs.slice(i + 1)
      .reduce((n, m) => n + (m.payload?.tool_calls?.length ?? 0), 0);
  }
  const isEditing = (m: { id?: string }) => editing !== null && !!m.id && m.id === editing;
  function cancelEdit() { editing = null; draft = ''; }

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
      {#if !user && m.id && sessionStore.logs[m.id]?.length}
        <TurnLog events={sessionStore.logs[m.id]} />
      {/if}
      {#if user && isEditing(m)}
        <div class="edit">
          <div class="edit-title">
            <Pencil size={12} />
            <span>Editing — Cerveau will answer this again</span>
          </div>
          <textarea bind:value={draft} rows="3" aria-label="edit message"
            onkeydown={(e) => {
              if (e.key === 'Escape') cancelEdit();
              if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); commitEdit(m.id ?? ''); }
            }}></textarea>
          <div class="edit-row">
            <span class="edit-note">
              {#if editing && toolsAfter(editing) > 0}
                the reply below is replaced — files already written stay on disk
              {:else}
                the reply below is replaced
              {/if}
            </span>
            <button class="ebtn" onclick={cancelEdit}>Cancel</button>
            <button class="ebtn go" disabled={!draft.trim()} onclick={() => commitEdit(m.id ?? '')}>
              Send
            </button>
          </div>
        </div>
      {:else if user}
        <span class="utext">{m.payload?.text ?? ''}</span>
      {:else}
        {@const live = sessionStore.running && i === visible.length - 1}
        {@const md = live ? splitStreaming(m.payload?.text ?? '') : null}
        {#if md && md.pending}
          <!-- Mid-stream: render the completed blocks and hold the unfinished
               tail as plain text. A half-open fence otherwise renders broken
               and reflows the whole message when it closes. -->
          <Markdown source={md.ready} />
          <span class="pending">{md.pending}</span>
        {:else}
          <Markdown source={m.payload?.text ?? ''} />
        {/if}
      {/if}
    </div>

    {#if !isEditing(m)}
      {@const ckey = m.id ?? `${m.ts}-${i}`}
      <div class="acts">
        <button class="act" onclick={() => copy(ckey, m.payload?.text ?? '')}
          use:tooltip={copied === ckey ? 'copied' : 'copy this message'}
          aria-label="copy message">
          {#if copied === ckey}<Check size={13} />{:else}<Copy size={13} />{/if}
        </button>
        {#if user && m.id && !sessionStore.running}
          <button class="act" onclick={() => startEdit(m.id!, m.payload?.text ?? '')}
            use:tooltip={'edit this message — Cerveau answers again from here, replacing what came after'}
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

  /* the still-arriving tail: same metrics as rendered prose so releasing it
     into markdown does not jump the layout */
  .pending {
    white-space: pre-wrap; font-size: 13.5px; line-height: 1.55; color: var(--text);
  }

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
  /* Name the mode. Without a label the editor is just a textarea that appeared
     where a message used to be, so a mis-click reads as a rendering bug rather
     than as a state the user entered. */
  .edit-title {
    display: flex; align-items: center; gap: 6px;
    font-size: 11px; letter-spacing: .04em; color: var(--accent);
  }
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
    background: none; border: 1px solid var(--line2); color: var(--dim);
  }
  .ebtn:hover { color: var(--text); border-color: var(--dim); }
  .ebtn.go { background: var(--accent); border-color: var(--accent); color: var(--on-accent); font-weight: 600; }
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
