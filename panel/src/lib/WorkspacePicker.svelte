<script lang="ts">
  // Remote folder picker. The desktop's native dialog opens on the MACHINE,
  // which a phone user cannot see or dismiss — so both platforms use this.
  import { Dialog } from 'bits-ui';
  import { Folder, CornerLeftUp, Check, X } from 'lucide-svelte';

  let { open = $bindable(false), current = '', onPick } = $props<{
    open?: boolean;
    current?: string;
    onPick: (path: string) => void;
  }>();

  interface Entry { name: string; path: string }
  interface Listing { root: string; path: string; parent: string; entries: Entry[] }

  let listing = $state<Listing | null>(null);
  let error = $state('');
  let loading = $state(false);

  async function load(path?: string): Promise<void> {
    loading = true; error = '';
    try {
      const q = path ? `?path=${encodeURIComponent(path)}` : '';
      const r = await fetch(`/api/fs/list${q}`);
      if (!r.ok) { error = await r.text(); return; }
      listing = await r.json();
    } catch {
      error = 'could not reach the machine';
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    if (open) void load(current || undefined);
    else { listing = null; error = ''; }
  });

  function choose(): void {
    if (!listing) return;
    onPick(listing.path);
    open = false;
  }

  /** show the path relative to home, which is what the user recognises */
  const pretty = $derived.by(() => {
    if (!listing) return '';
    const p = listing.path;
    return p.startsWith(listing.root) ? '~' + p.slice(listing.root.length) : p;
  });
</script>

<Dialog.Root bind:open>
  <Dialog.Portal>
    <Dialog.Overlay class="wp-overlay" />
    <Dialog.Content class="wp-content">
      <div class="sheet">
        <header>
          <Dialog.Title class="wp-title">Choose a workspace</Dialog.Title>
          <Dialog.Close class="x" aria-label="close"><X size={15} /></Dialog.Close>
        </header>

        <div class="crumb mono"><span>{pretty || '…'}</span></div>

        <div class="list">
          {#if error}
            <p class="err">{error}</p>
          {:else if loading && !listing}
            <p class="wait">loading…</p>
          {:else if listing}
            {#if listing.parent}
              <button class="row up" onclick={() => load(listing!.parent)}>
                <CornerLeftUp size={14} /><span>up</span>
              </button>
            {/if}
            {#each listing.entries as e (e.path)}
              <button class="row" onclick={() => load(e.path)}>
                <Folder size={14} /><span>{e.name}</span>
              </button>
            {:else}
              <p class="wait">no folders here</p>
            {/each}
          {/if}
        </div>

        <button class="use" onclick={choose} disabled={!listing}>
          <Check size={15} /> use this folder
        </button>
      </div>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<style>
  :global(.wp-overlay) {
    position: fixed; inset: 0; z-index: var(--z-modal);
    background: color-mix(in srgb, #000 62%, transparent); backdrop-filter: blur(3px);
  }
  :global(.wp-content) {
    position: fixed; inset: 0; z-index: var(--z-modal);
    display: grid; place-items: center; padding: 20px; pointer-events: none;
  }
  .sheet {
    pointer-events: auto;
    width: 100%; max-width: 420px; max-height: 72vh;
    display: flex; flex-direction: column;
    background: var(--surface-raised); border-radius: 16px;
    box-shadow: 0 0 0 1px var(--line2), 0 1px 0 0 var(--lift) inset;
    overflow: hidden;
  }
  header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 14px 14px 10px;
  }
  :global(.wp-title) { margin: 0; font-size: 13.5px; font-weight: 640; color: var(--text); }
  :global(.wp-content .x) {
    display: inline-flex; align-items: center; justify-content: center;
    width: 28px; height: 28px; border: none; border-radius: 8px;
    background: transparent; color: var(--faint); cursor: pointer;
  }
  :global(.wp-content .x:hover) { color: var(--text); background: color-mix(in srgb, #fff 6%, transparent); }

  .crumb {
    /* Truncate on the LEFT (the deep end matters) without direction:rtl,
       which reversed the whole path — it read "…/Equilibrium/~". */
    padding: 0 14px 10px; font-size: 11.5px; color: var(--dim);
    overflow: hidden; white-space: nowrap;
    display: flex; justify-content: flex-end;
  }
  .crumb span { overflow: hidden; text-overflow: ellipsis; }
  .list {
    flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain;
    border-top: 1px solid var(--line); border-bottom: 1px solid var(--line);
  }
  .row {
    display: flex; align-items: center; gap: 10px; width: 100%;
    padding: 12px 14px; border: none; background: transparent;
    color: var(--muted); font-size: 13px; text-align: left; cursor: pointer;
    min-height: 44px;   /* touch target */
  }
  .row:hover { background: color-mix(in srgb, #fff 4%, transparent); color: var(--text); }
  .row.up { color: var(--dim); }
  .row span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .use {
    display: flex; align-items: center; justify-content: center; gap: 8px;
    margin: 12px 14px 14px; padding: 12px; min-height: 46px;
    border: none; border-radius: 10px; cursor: pointer;
    background: var(--accent); color: var(--accent-ink); font-weight: 600; font-size: 13px;
  }
  .use:disabled { opacity: .4; cursor: default; }
  .wait, .err { padding: 20px 14px; font-size: 12.5px; color: var(--muted); text-align: center; }
  .err { color: var(--err); }
</style>
