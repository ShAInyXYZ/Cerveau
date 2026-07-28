<script>
  import { AlertTriangle, X, Feather, Flame, Check, Trash2 } from 'lucide-svelte';

  let { session, preview, onConfirm, onClose } = $props();

  let mode = $state('session');   // 'session' (light) | 'all' (hard)
  let typed = $state('');
  let busy = $state(false);

  const expected = $derived(preview ? `${preview.name}#${preview.code}` : '');
  const matches = $derived(typed.trim() === expected);

  async function doDelete() {
    if (!matches || busy) return;
    busy = true;
    await onConfirm?.({ mode, confirm: typed.trim() });
    busy = false;
  }
</script>

<div class="overlay" onclick={onClose} role="presentation">
  <div class="stack" onclick={(e) => e.stopPropagation()}>
    <div class="dialog" role="dialog" aria-modal="true">
      <button class="x" onclick={onClose} aria-label="close"><X size={16} /></button>

      <div class="head">
        <span class="hicon"><AlertTriangle size={18} strokeWidth={2.2} /></span>
        <div class="htext">
          <h2>Delete “{preview?.name ?? session?.name}”</h2>
          <p>This can’t be undone.</p>
        </div>
      </div>

      <div class="tiers">
        <button class="tier light" class:on={mode === 'session'} onclick={() => (mode = 'session')}>
          <Feather size={28} strokeWidth={1.6} />
          <span class="t-title">Light</span>
          <span class="t-sub">Clears the conversation and its raw history, but keeps the distilled summaries it learned.</span>
        </button>
        <button class="tier hard" class:on={mode === 'all'} onclick={() => (mode = 'all')}>
          <Flame size={28} strokeWidth={1.6} />
          <span class="t-title">Hard</span>
          <span class="t-sub">Erases the session and every memory it produced. Nothing is kept.</span>
        </button>
      </div>
    </div>

    <!-- confirm floats BELOW the dialog, in the chat-bar identity -->
    <div class="confirm">
      <div class="clabel">Type <code>{expected}</code> to confirm</div>
      <div class="cbar" class:ok={matches}>
        <input class="cinput" bind:value={typed} placeholder={expected}
          onkeydown={(e) => e.key === 'Enter' && doDelete()}
          autocomplete="off" spellcheck="false" />
        {#if matches}<span class="cok"><Check size={16} strokeWidth={2.8} /></span>{/if}
        <button class="del" class:hard={mode === 'all'} disabled={!matches || busy} onclick={doDelete}
          aria-label="delete">
          <Trash2 size={17} strokeWidth={2.1} />
        </button>
      </div>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed; inset: 0; z-index: 200;
    background: color-mix(in srgb, #000 58%, transparent); backdrop-filter: blur(3px);
    display: flex; align-items: center; justify-content: center; padding: 28px;
    animation: fade .15s ease;
  }
  /* dialog + the detached confirm bar, stacked */
  .stack {
    display: flex; flex-direction: column; align-items: stretch; gap: 16px;
    width: 100%; max-width: 460px;
    animation: rise .2s cubic-bezier(.16,1,.3,1);
  }
  .dialog {
    position: relative; width: 100%;
    background: linear-gradient(180deg, color-mix(in srgb,#fff 2.5%, var(--s1)) 0%, var(--s1) 40%);
    border-radius: 18px;
    box-shadow: 0 0 0 1px var(--line2), 0 30px 80px -24px rgba(0,0,0,.85);
    padding: 24px 26px;
  }
  .x {
    position: absolute; top: 16px; right: 16px;
    display: inline-flex; align-items: center; justify-content: center;
    width: 28px; height: 28px; border: none; border-radius: 8px;
    background: transparent; color: var(--faint); cursor: pointer; transition: color .12s, background .12s;
  }
  .x:hover { color: var(--text); background: color-mix(in srgb,#fff 6%,transparent); }

  /* header */
  .head { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; padding-right: 26px; }
  .hicon {
    flex-shrink: 0; width: 36px; height: 36px; border-radius: 10px;
    display: inline-flex; align-items: center; justify-content: center;
    color: var(--err); background: color-mix(in srgb, var(--err) 13%, transparent);
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--err) 26%, transparent);
  }
  .htext h2 { margin: 0; font-size: 16px; font-weight: 640; color: var(--text); letter-spacing: -.01em; }
  .htext p { margin: 3px 0 0; font-size: 12.5px; color: var(--muted); }

  /* tiers — side by side, icon is the hero, frame colour signals active */
  .tiers { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-bottom: 18px; }
  .tier {
    --c: var(--accent);
    display: flex; flex-direction: column; align-items: center; text-align: center; gap: 7px;
    padding: 20px 16px 18px; border-radius: 14px; cursor: pointer;
    background: var(--s2); border: 1.5px solid var(--line2); color: var(--dim);
    transition: border-color .14s, background .14s, color .14s;
  }
  .tier.hard { --c: var(--err); }
  .tier:hover { color: var(--muted); border-color: var(--faint); }
  .tier.on {
    color: var(--c); border-color: var(--c);
    background: color-mix(in srgb, var(--c) 9%, var(--s2));
  }
  .t-title { font-size: 15px; font-weight: 640; color: var(--text); margin-top: 3px; }
  .t-sub { font-size: 11.5px; color: var(--muted); line-height: 1.5; }
  .tier.on .t-sub { color: color-mix(in srgb, var(--c) 45%, var(--muted)); }

  /* confirm — its own framed block below the dialog, centered instruction + the
     chat-bar pill input inside it */
  .confirm {
    display: flex; flex-direction: column; gap: 12px;
    padding: 16px; border-radius: 18px;
    background: linear-gradient(180deg, color-mix(in srgb,#fff 2.5%, var(--s1)) 0%, var(--s1) 40%);
    box-shadow: 0 0 0 1px var(--line2), 0 30px 80px -24px rgba(0,0,0,.85);
  }
  .clabel { font-size: 12.5px; color: var(--muted); text-align: center; }
  .clabel code {
    font-family: var(--font-mono); font-size: 11.5px; color: var(--accent);
    background: var(--s3); padding: 2px 7px; border-radius: 6px;
    box-shadow: inset 0 0 0 1px var(--line2); user-select: all;
  }
  /* the chat-bar pill, exactly */
  .cbar {
    display: flex; align-items: center; gap: 8px;
    background: var(--surface-raised); border-radius: 999px;
    box-shadow: var(--elev-2); padding: 6px 6px 6px 18px;
    transition: box-shadow .14s;
  }
  .cbar.ok { box-shadow: 0 0 0 1px color-mix(in srgb,var(--ok) 45%,transparent), var(--elev-2); }
  .cinput {
    flex: 1; min-width: 0; font-family: var(--font-mono); font-size: 13.5px; color: var(--text);
    background: transparent; border: none; outline: none; padding: 9px 0;
  }
  .cok { display: inline-flex; color: var(--ok); flex-shrink: 0; margin-right: 2px; }
  /* the send button, exactly — amber circle, red when Hard */
  .del {
    flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center;
    width: 42px; height: 42px; border-radius: 50%; cursor: pointer;
    border: 1px solid var(--accent); background: var(--accent); color: var(--accent-ink);
    transition: filter .1s, transform .05s, background .1s, border-color .1s, opacity .12s;
  }
  .del.hard { background: var(--err); border-color: var(--err); color: #fff; }
  .del:hover:not(:disabled) { filter: brightness(1.08); }
  .del:active:not(:disabled) { transform: scale(.94); }
  .del:disabled { opacity: .35; cursor: default; }

  @keyframes fade { from { opacity: 0; } to { opacity: 1; } }
  @keyframes rise { from { opacity: 0; transform: translateY(10px) scale(.97); } to { opacity: 1; transform: none; } }
  @media (prefers-reduced-motion: reduce) { .overlay, .dialog { animation: none; } }
</style>
