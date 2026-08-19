<script lang="ts">
  import { PanelLeftOpen, PanelLeftClose } from 'lucide-svelte';
  import './tokens.css';
  import StatusBar from './lib/StatusBar.svelte';
  import WorkspaceRail from './lib/WorkspaceRail.svelte';
  import Chat from './lib/chat/Chat.svelte';
  import Activity from './lib/Activity.svelte';
  import MemoryView from './lib/MemoryView.svelte';
  import RfxDock from './lib/RfxDock.svelte';
  import Settings from './lib/Settings.svelte';
  import DeleteSessionDialog from './lib/DeleteSessionDialog.svelte';
  import { api, onAuthRequired, setAuthToken } from './lib/api';
  import { healthStore } from './lib/stores/health.svelte.ts';
  import { sessionStore } from './lib/stores/session.svelte.ts';
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
    sessionStore.start();
    return () => { healthStore.stop(); sessionStore.stop(); };
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

  <div class="body">
    <div class="railwrap" class:open={uiStore.railOpen}>
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
        {#key healthStore.workspace}
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
  .body { flex: 1; display: flex; min-height: 0; gap: 1px; background: var(--line); position: relative; }
  .body > :global(*) { background: var(--bg); }

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

  /* desktop keeps the rail permanently open, so the toggle is phone-only */
  .railtoggle { display: none; }

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
