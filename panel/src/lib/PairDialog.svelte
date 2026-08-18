<script lang="ts">
  // "Pair a device" — the desktop half of the device-access flow.
  // Not phone-specific: the same invitation enrolls a laptop just as well.
  // Asks the core for a short-lived invitation, shows the QR + the 6-char
  // code, and counts down its expiry. The phone scans or types it.
  import { Dialog } from 'bits-ui';
  import { X, MonitorSmartphone, RefreshCw } from 'lucide-svelte';

  let { open = $bindable(false) } = $props<{ open?: boolean }>();

  interface Invite { code: string; qr: string; gate: string; slug: string; expires_in: number }

  let invite = $state<Invite | null>(null);
  let error = $state('');
  let left = $state(0);
  let timer: ReturnType<typeof setInterval> | null = null;

  async function mint(): Promise<void> {
    error = ''; invite = null;
    try {
      const r = await fetch('/api/pair/invite', { method: 'POST' });
      if (!r.ok) { error = `the core refused to mint an invitation (${r.status})`; return; }
      invite = await r.json();
      left = invite?.expires_in ?? 0;
    } catch {
      error = 'could not reach the core';
    }
  }

  $effect(() => {
    if (!open) {
      if (timer) { clearInterval(timer); timer = null; }
      return;
    }
    void mint();
    timer = setInterval(() => {
      left = Math.max(0, left - 1);
      if (left === 0 && invite) invite = null; // expired: force a re-mint
    }, 1000);
    return () => { if (timer) { clearInterval(timer); timer = null; } };
  });

  const mmss = $derived(
    `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`,
  );
</script>

<Dialog.Root bind:open>
  <Dialog.Portal>
    <Dialog.Overlay class="pd-overlay" />
    <Dialog.Content class="pd-content">
      <div class="card">
        <Dialog.Close class="x" aria-label="close"><X size={16} /></Dialog.Close>

        <header>
          <span class="hicon"><MonitorSmartphone size={17} /></span>
          <div>
            <Dialog.Title class="pd-title">Pair a device</Dialog.Title>
            <Dialog.Description class="pd-desc">
              On the new device, open Cerveau and choose to pair, then scan or type this.
            </Dialog.Description>
          </div>
        </header>

        {#if error}
          <p class="err">{error}</p>
          <button class="again" onclick={mint}><RefreshCw size={13} /> try again</button>
        {:else if !invite}
          <p class="wait">minting a one-time invitation…</p>
        {:else}
          <div class="code mono">{invite.code}</div>
          <div class="qrwrap"><img src={invite.qr} alt="pairing QR code" width="220" height="220" /></div>
          <p class="meta">
            expires in <strong>{mmss}</strong> · one use ·
            <span class="mono">{invite.gate}/p/{invite.slug}</span>
          </p>
          <button class="again" onclick={mint}><RefreshCw size={13} /> new code</button>
        {/if}
      </div>
    </Dialog.Content>
  </Dialog.Portal>
</Dialog.Root>

<style>
  :global(.pd-overlay) {
    position: fixed; inset: 0; z-index: var(--z-modal);
    background: color-mix(in srgb, #000 60%, transparent); backdrop-filter: blur(3px);
  }
  :global(.pd-content) {
    position: fixed; inset: 0; z-index: var(--z-modal);
    display: grid; place-items: center; padding: 24px; pointer-events: none;
  }
  .card {
    pointer-events: auto; position: relative;
    width: 100%; max-width: 340px; text-align: center;
    padding: 24px 22px;
    background: var(--surface-raised); border-radius: 16px;
    box-shadow: 0 0 0 1px var(--line2), 0 1px 0 0 var(--lift) inset;
  }
  :global(.pd-content .x) {
    position: absolute; top: 12px; right: 12px;
    display: inline-flex; align-items: center; justify-content: center;
    width: 28px; height: 28px; border: none; border-radius: 8px;
    background: transparent; color: var(--faint); cursor: pointer;
  }
  :global(.pd-content .x:hover) { color: var(--text); background: color-mix(in srgb, #fff 6%, transparent); }

  header { display: flex; gap: 11px; text-align: left; margin-bottom: 18px; padding-right: 24px; }
  .hicon {
    flex-shrink: 0; width: 34px; height: 34px; border-radius: 9px;
    display: inline-flex; align-items: center; justify-content: center;
    color: var(--accent); background: var(--accent-soft);
    box-shadow: inset 0 0 0 1px var(--accent-line);
  }
  :global(.pd-title) { margin: 0; font-size: 14px; font-weight: 640; color: var(--text); }
  :global(.pd-desc) { margin: 3px 0 0; font-size: 11.5px; color: var(--muted); line-height: 1.45; }

  .code {
    font-size: 34px; font-weight: 600; letter-spacing: .26em;
    color: var(--accent); padding-left: .26em; margin-bottom: 14px;
  }
  .qrwrap { background: #fff; padding: 10px; border-radius: 10px; display: inline-block; }
  .meta { font-size: 11px; color: var(--dim); margin: 16px 0 0; line-height: 1.6; }
  .meta strong { color: var(--text); font-weight: 600; }
  .meta .mono { display: block; color: var(--faint); font-size: 10.5px; margin-top: 3px; }
  .wait { color: var(--muted); font-size: 12.5px; padding: 28px 0; }
  .err { color: var(--err); font-size: 12.5px; padding: 18px 0 8px; }
  .again {
    display: inline-flex; align-items: center; gap: 6px; margin-top: 14px;
    background: transparent; border: none; cursor: pointer;
    color: var(--dim); font-size: 11.5px;
  }
  .again:hover { color: var(--text); }
</style>
