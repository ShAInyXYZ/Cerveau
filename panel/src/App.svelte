<script lang="ts">
  import { Moon, Coffee, PanelLeftOpen, PanelLeftClose } from 'lucide-svelte';
  // Inter, bundled — not fetched from Google. The token has named Inter since
  // the start while nothing ever loaded it, so the panel has been rendering in
  // whatever system-ui resolves to (DejaVu Sans on this machine).
  //
  // Every subset ships, not just latin: @font-face carries unicode-range, so a
  // browser downloads only the ranges the text on screen actually uses. Cyrillic
  // and Greek cost disk in the binary, not bytes on the wire, and a session that
  // needs them renders instead of falling back mid-sentence.
  // Manrope, bundled — not fetched from Google, so the panel still renders
  // with no internet. Ships cyrillic, cyrillic-ext and greek alongside latin,
  // so a Russian or Greek word stays in the SAME typeface instead of switching
  // mid-sentence.
  import '@fontsource-variable/manrope';
  import './tokens.css';
  import StatusBar from './lib/StatusBar.svelte';
  import WorkspaceRail from './lib/WorkspaceRail.svelte';
  import Chat from './lib/chat/Chat.svelte';
  import Activity from './lib/Activity.svelte';
  import MemoryView from './lib/MemoryView.svelte';
  import RfxDock from './lib/RfxDock.svelte';
  import Settings from './lib/Settings.svelte';
  import IdleScreen from './lib/IdleScreen.svelte';
  import DeleteSessionDialog from './lib/DeleteSessionDialog.svelte';
  import { api, onAuthRequired, setAuthToken, type IdleStatus } from './lib/api';
  import { healthStore } from './lib/stores/health.svelte.ts';
  import { idleStore } from './lib/stores/idle.svelte.ts';
  import { sessionStore } from './lib/stores/session.svelte.ts';
  import { tooltip } from './kit/tooltip.js';
  import { uiStore } from './lib/stores/ui.svelte.ts';
  import type { SessionMeta } from './lib/types';

  // ── lock screen: the core is paired (token set), panel needs the token ──
  let locked = $state(false);
  let tokenInput = $state('');
  let tokenErr = $state('');
  onAuthRequired(() => (locked = true));
  async function unlock(): Promise<void> {
    setAuthToken(tokenInput.trim());
    const h = await api.health();
    if (h) { locked = false; tokenErr = ''; location.reload(); }
    else { setAuthToken(null); tokenErr = 'wrong token'; }
  }

  $effect(() => {
    healthStore.start();
    idleStore.start();
    sessionStore.start();
    return () => { healthStore.stop(); idleStore.stop(); sessionStore.stop(); };
  });

  // ── delete-session flow (dialog state is view-local, not store-worthy) ──
  let deleteTarget = $state<SessionMeta | null>(null);
  let deletePreview = $state<unknown>(null);
  async function openDelete(s: SessionMeta): Promise<void> {
    deleteTarget = s;
    deletePreview = await api.deletePreview(s.id);
  }
  function closeDelete(): void { deleteTarget = null; deletePreview = null; }
  async function confirmDelete({ mode, confirm }: { mode: string; confirm: string }): Promise<void> {
    if (!deleteTarget) return;
    if (await sessionStore.remove(deleteTarget.id, mode, confirm)) closeDelete();
  }

  // selecting a session from the rail also closes the mobile drawer
  function select(id: string): void {
    sessionStore.select(id);
    uiStore.closeRail();
  }
</script>

<div class="shell">
  <StatusBar health={healthStore.value} windowReport={sessionStore.windowReport}
    bind:activityOpen={
      () => uiStore.activityOpen,
      (v) => (uiStore.activityOpen = v)
    }
    memView={uiStore.view === 'memory'}
    onToggleMemory={() => uiStore.toggleMemory()}
    />

  {#if healthStore.offline}
    <div class="offline" role="alert">
      <span class="offline-dot"></span>
      core unreachable — retrying. Start it with <code>crv</code> if it isn't running.
    </div>
  {/if}

  {#if idleStore.notice}
    <div class="idle-notice" role="status" aria-atomic="true">
      <span class="idle-notice-icon" aria-hidden="true">
        {#if idleStore.value?.state === 'waking'}
          <Coffee size={20} strokeWidth={1.6} />
        {:else}
          <Moon size={20} strokeWidth={1.6} />
        {/if}
      </span>
      <p>{idleStore.notice}</p>
    </div>
  {/if}

  {#if idleStore.visible}
    <div class="idlewrap">
      <IdleScreen status={idleStore.value} onchange={(s: IdleStatus) => idleStore.set(s)} />
    </div>
  {/if}

  <div class="body">
    <div class="railwrap" class:open={uiStore.railOpen} class:collapsed={uiStore.railCollapsed}>
      <WorkspaceRail sessions={sessionStore.sessions} activeId={sessionStore.activeId}
        runningIds={sessionStore.runningIds}
        lastEvents={sessionStore.lastEvents} skills={sessionStore.skills}
        activeWorkspace={healthStore.workspace}
        onSelect={select}
        onCreate={(name: string, ws?: string) => sessionStore.create(name, ws)}
        onRename={(id: string, name: string) => sessionStore.rename(id, name)}
        onDelete={openDelete}
        onInstant={() => sessionStore.createInstant()}
        onSettings={() => uiStore.toggleSettings()}
        settingsOpen={uiStore.view === 'settings'} />
    </div>
    <!-- The drawer toggle rides the RAIL's edge rather than sitting in the
         header: the control stays attached to the column it opens and closes,
         so its meaning is positional instead of learned. -->
    <!-- Desktop: folds the rail away to reclaim its width. Separate from the
         mobile drawer handle below — that one slides an overlay over the
         content, this one changes the layout. -->
    <button class="railfold" class:collapsed={uiStore.railCollapsed}
      onclick={() => uiStore.toggleRailCollapsed()}
      aria-label={uiStore.railCollapsed ? 'show the project tree' : 'hide the project tree'}
      aria-expanded={!uiStore.railCollapsed}
      use:tooltip={uiStore.railCollapsed ? 'show the project tree' : 'hide the project tree'}>
      {#if uiStore.railCollapsed}<PanelLeftOpen size={15} />{:else}<PanelLeftClose size={15} />{/if}
    </button>

    <button class="railtoggle" class:open={uiStore.railOpen}
      onclick={() => uiStore.toggleRail()}
      aria-label={uiStore.railOpen ? 'close the session drawer' : 'open the session drawer'}
      aria-expanded={uiStore.railOpen}>
      {#if uiStore.railOpen}<PanelLeftClose size={16} />{:else}<PanelLeftOpen size={16} />{/if}
    </button>

    {#if uiStore.railOpen}
      <button class="scrim" aria-label="close the session drawer" onclick={() => uiStore.closeRail()}></button>
    {/if}

    {#if uiStore.view === 'settings'}
      <Settings />
    {:else if uiStore.view === 'memory'}
      <MemoryView />
    {:else}
      <div class="chatwrap">
        <Chat />
        {#key `${sessionStore.activeId}:${sessionStore.workspace}`}
          <RfxDock sessionId={sessionStore.activeId}
            onTurn={(text: string, m?: string) => sessionStore.panelTurn(text, m as never)} />
        {/key}
      </div>
    {/if}

    {#if uiStore.activityOpen}
      <Activity ticks={sessionStore.ticks} running={sessionStore.running}
        onClose={() => (uiStore.activityOpen = false)} />
    {/if}
  </div>
</div>

{#if deleteTarget && deletePreview}
  <DeleteSessionDialog session={deleteTarget} preview={deletePreview}
    onConfirm={confirmDelete} onClose={closeDelete} />
{/if}

{#if locked}
  <div class="lock" role="dialog" aria-label="cerveau locked">
    <div class="lockcard">
      <div class="lock-mark">◈</div>
      <div class="label">CERVEAU LOCKED</div>
      <p>This instance is paired. Enter the access token to unlock.</p>
      <input type="password" bind:value={tokenInput} placeholder="access token"
        onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && unlock()} autocomplete="off" />
      {#if tokenErr}<div class="lock-err">{tokenErr}</div>{/if}
      <button onclick={unlock}>unlock</button>
    </div>
  </div>
{/if}

<style>
  .lock {
    position: fixed; inset: 0; z-index: var(--z-modal);
    background: var(--bg);
    display: flex; align-items: center; justify-content: center;
  }
  .lockcard {
    display: flex; flex-direction: column; gap: 12px; align-items: center;
    max-width: 320px; padding: 28px; text-align: center;
  }
  .lock-mark { color: var(--accent); font-size: 28px; }
  .lockcard p { color: var(--muted); font-size: 12px; }
  .lockcard input {
    width: 100%; padding: 10px 12px; background: var(--s2); color: var(--text);
    border: 1px solid var(--line2); border-radius: var(--r);
    font-family: var(--font-mono); font-size: 12px; outline: none;
  }
  .lockcard input:focus { border-color: var(--accent-line); }
  .lock-err { color: var(--err); font-size: 11px; font-family: var(--font-mono); }
  .lockcard button {
    width: 100%; padding: 10px; background: var(--accent); color: var(--accent-ink);
    border: none; border-radius: var(--r); cursor: pointer;
    font-family: var(--font-mono); font-size: 11px; letter-spacing: .18em;
    text-transform: uppercase;
  }

  .shell { height: 100vh; height: 100dvh; display: flex; flex-direction: column; background: var(--bg); }
  .chatwrap { display: flex; flex: 1; min-width: 0; min-height: 0; }
  .chatwrap :global(main.chat) { flex: 1; min-width: 0; }
  @media (max-width: 900px) { .chatwrap { flex-direction: column; } }
  .body { flex: 1; display: flex; min-height: 0; gap: 1px; background: var(--line); position: relative; }
  .body > :global(*) { background: var(--bg); }

  .idlewrap {
    /* An interruption, not a layout member: it floats in the centre of the
       screen instead of pushing the whole app down by its own height.
       The wrapper ignores the pointer so the countdown never blocks the work
       that would cancel it — typing a message keeps the Core awake by itself.
       Only the card takes clicks. */
    position: fixed;
    inset: 0;
    z-index: var(--z-modal);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
    pointer-events: none;
  }
  .idlewrap > :global(*) { pointer-events: auto; }

  .idle-notice {
    /* A passive popup, never a shell row or another blocking idle dialog. */
    position: fixed;
    top: calc(var(--bar-h) + var(--sp-5) + env(safe-area-inset-top, 0px));
    left: 50%;
    transform: translateX(-50%);
    z-index: var(--z-popover);
    width: max-content;
    max-width: min(380px, calc(100% - 32px));
    display: flex;
    align-items: center;
    gap: var(--sp-5);
    padding: var(--sp-6) var(--sp-7);
    border: 1px solid var(--line2);
    border-radius: 16px;
    background: var(--s2);
    box-shadow: var(--elev-2), 0 8px 24px rgb(0 0 0 / .18);
    color: var(--text);
    font-size: var(--fs-body);
    line-height: 1.55;
    pointer-events: none;
  }
  .idle-notice-icon { flex: none; color: var(--muted); }
  .idle-notice p { min-width: 0; overflow-wrap: anywhere; }
  @supports (backdrop-filter: blur(12px)) {
    .idle-notice {
      background: color-mix(in srgb, var(--s2) 94%, transparent);
      backdrop-filter: blur(12px);
    }
  }

  .offline {
    display: flex; align-items: center; gap: 8px;
    padding: 6px 14px;
    background: color-mix(in srgb, var(--err) 12%, var(--bg));
    color: var(--text); font-size: 12px;
    border-bottom: 1px solid color-mix(in srgb, var(--err) 40%, transparent);
  }
  .offline code { font-family: var(--font-mono); color: var(--err); }
  .offline-dot {
    width: 7px; height: 7px; border-radius: 50%; background: var(--err);
    animation: pulse 1.2s ease-in-out infinite;
  }
  @keyframes pulse { 0%,100% { opacity: 1; } 50% { opacity: .3; } }

  .railwrap { display: flex; min-height: 0; }
  .scrim { display: none; }

  /* desktop keeps the rail permanently open, so the DRAWER toggle is
     phone-only — the fold control below replaces it there */
  .railtoggle { display: none; }

  /* ── desktop: fold the rail away, reclaiming its width ── */
  .railwrap.collapsed {
    width: 0; overflow: hidden;
    transition: width var(--t-med) var(--ease-out);
  }
  .railfold {
    position: absolute; left: 0; top: 10px; z-index: var(--z-raised);
    display: flex; align-items: center; justify-content: center;
    width: 22px; height: 34px; padding: 0;
    background: var(--s2); color: var(--faint);
    border: none; border-radius: 0 8px 8px 0;
    cursor: pointer;
    transition: transform var(--t-med) var(--ease-out), color var(--t-fast), opacity var(--t-fast);
    /* rides the rail's edge, so it reads as belonging to the column it folds */
    transform: translateX(var(--rail-w, 260px));
    opacity: 0;
  }
  /* stays out of the way until wanted; a control that never fades is a
     permanent fixture, and this one is used rarely */
  .railwrap:hover ~ .railfold,
  .railfold:hover, .railfold:focus-visible { opacity: 1; }
  .railfold:hover { color: var(--text); }
  .railfold.collapsed { transform: none; opacity: 1; color: var(--dim); }

  /* ── compact: the rail becomes an overlay drawer ── */
  @media (max-width: 900px) {
    .railwrap {
      position: absolute; inset: 0 auto 0 0; z-index: var(--z-overlay);
      transform: translateX(-100%);
      transition: transform var(--t-med) var(--ease-out);
      box-shadow: var(--elev-2);
    }
    .railwrap.open { transform: none; }
    .scrim {
      display: block; position: absolute; inset: 0;
      z-index: calc(var(--z-overlay) - 1);
      background: rgba(0, 0, 0, .5);
      border: none; cursor: pointer;
    }

    /* the drawer handle owns this job below 900px; two controls for one
       column would be a choice the user has to decode */
    .railfold { display: none; }
    /* a rail collapsed on desktop must not stay collapsed on a phone, where
       the drawer is the only way to reach it */
    .railwrap.collapsed { width: auto; overflow: visible; }

    .railtoggle {
      display: flex; align-items: center; justify-content: center;
      position: absolute; left: 0; top: 8px;
      z-index: var(--z-overlay);
      width: 34px; height: 40px;
      padding: 0; border: none; cursor: pointer;
      background: var(--s2);
      color: var(--dim);
      border-radius: 0 10px 10px 0;
      box-shadow: var(--elev-1);
      transition: transform var(--t-med) var(--ease-out), color var(--t-fast);
    }
    .railtoggle:hover, .railtoggle:focus-visible { color: var(--text); }
    /* travels with the drawer so it always hugs the column's edge */
    .railtoggle.open { transform: translateX(var(--rail-w, 260px)); }
  }
</style>
