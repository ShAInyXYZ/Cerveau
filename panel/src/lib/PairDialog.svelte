<script lang="ts">
  // "Pair a device" — the desktop half of the device-access flow.
  // Not phone-specific: the same invitation enrolls a laptop just as well.
  // Asks the core for a short-lived invitation, shows the QR + the 6-char
  // code, and counts down its expiry. The phone scans or types it.
  import { Dialog } from 'bits-ui';
  import { X, MonitorSmartphone, RefreshCw, QrCode, Trash2 } from 'lucide-svelte';
  import { tooltip } from '../kit/tooltip.js';

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

  type Device = {
    id: string; label?: string; added_at: string; last_seen?: string;
    approved_by?: string; approver_gone?: boolean; self?: boolean;
  };
  let devices = $state<Device[]>([]);
  let devErr = $state('');
  let revoking = $state('');

  async function loadDevices(): Promise<void> {
    devErr = '';
    try {
      const r = await fetch('/api/devices');
      if (!r.ok) {
        // 401 here is normal and not an error to shout about: listing needs a
        // trusted device signature, which a desktop browser does not have.
        devErr = r.status === 401 ? '' : `could not read the device list (${r.status})`;
        devices = [];
        return;
      }
      devices = (await r.json()).devices ?? [];
    } catch {
      devErr = 'could not reach the core';
    }
  }

  async function revoke(id: string): Promise<void> {
    revoking = id;
    try {
      const r = await fetch('/api/devices/revoke', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, cascade: true }),
      });
      if (!r.ok) { devErr = `revoke failed (${r.status})`; return; }
      await loadDevices();
    } finally { revoking = ''; }
  }

  // "2 min ago" beats a timestamp for the only question being asked: is this
  // device still in use, or is it safe to revoke?
  function ago(iso?: string): string {
    if (!iso) return 'never used';
    const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
    if (s < 60) return 'active now';
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    return `${Math.floor(s / 86400)} d ago`;
  }
  const isLive = (d: Device) =>
    !!d.last_seen && Date.now() - new Date(d.last_seen).getTime() < 120_000;

  $effect(() => {
    if (!open) {
      if (timer) { clearInterval(timer); timer = null; }
      invite = null; error = '';   // never reopen showing a stale code
      return;
    }
    // Deliberately NOT minting here. Opening this dialog to see which devices
    // are paired should not put a live credential on screen; a code nobody
    // asked for is one more thing that can be read over a shoulder.
    void loadDevices();
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
          <!-- No code until asked for. Opening this to check the fleet should
               not put a live credential on screen. -->
          <button class="mintbtn" onclick={mint}>
            <QrCode size={15} /> Mint a pairing code
          </button>
        {:else}
          <div class="code mono">{invite.code}</div>
          <div class="qrwrap"><img src={invite.qr} alt="pairing QR code" width="220" height="220" /></div>
          <p class="meta">
            expires in <strong>{mmss}</strong> · one use ·
            <span class="mono">{invite.gate}/p/{invite.slug}</span>
          </p>
          <button class="again" onclick={mint}><RefreshCw size={13} /> new code</button>
        {/if}

        {#if devices.length || devErr}
          <div class="devices">
            <div class="dhead">
              <span>Paired devices</span>
              <span class="dcount">{devices.length}</span>
            </div>
            {#if devErr}<p class="err small">{devErr}</p>{/if}
            {#each devices as d (d.id)}
              <div class="drow">
                <span class="ddot" class:live={isLive(d)}></span>
                <span class="dname">
                  {d.label || d.id.slice(0, 8)}
                  {#if d.self}<span class="dself">this device</span>{/if}
                </span>
                <span class="dseen" class:live={isLive(d)}>{ago(d.last_seen)}</span>
                <button class="drevoke" disabled={revoking === d.id}
                  onclick={() => revoke(d.id)}
                  use:tooltip={d.self
                    ? 'revoking this device signs you out of it'
                    : 'revoke — also removes any device this one vouched for'}
                  aria-label="revoke {d.label || d.id}">
                  <Trash2 size={13} />
                </button>
              </div>
              {#if d.approver_gone}
                <p class="dorphan">its approver was revoked</p>
              {/if}
            {/each}
          </div>
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
  /* ── mint ── */
  .mintbtn {
    display: inline-flex; align-items: center; gap: 8px;
    margin: 4px auto 0; padding: 11px 20px;
    background: var(--accent); color: var(--on-accent);
    border: none; border-radius: 10px; cursor: pointer;
    font: inherit; font-size: 13.5px; font-weight: 600;
  }
  .mintbtn:hover { filter: brightness(1.06); }

  /* ── paired devices ── */
  .devices {
    margin-top: 20px; padding-top: 16px;
    border-top: 1px solid var(--line);
    text-align: left;
  }
  .dhead {
    display: flex; align-items: center; justify-content: space-between;
    font-size: 11px; letter-spacing: .06em; text-transform: uppercase;
    color: var(--dim); margin-bottom: 10px;
  }
  .dcount { font-family: var(--font-mono); }
  .drow { display: flex; align-items: center; gap: 9px; padding: 7px 0; }
  .ddot {
    width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0;
    background: var(--faint);
  }
  /* a signature inside the last two minutes — the only honest signal of a
     device being present, since nothing keeps a connection open */
  .ddot.live { background: var(--ok); }
  .dname {
    flex: 1; min-width: 0; font-size: 12.5px; color: var(--text);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .dself {
    margin-left: 6px; font-size: 9px; letter-spacing: .07em;
    text-transform: uppercase; color: var(--accent);
  }
  .dseen { font-size: 11px; color: var(--faint); flex-shrink: 0; }
  .dseen.live { color: var(--ok); }
  .drevoke {
    flex-shrink: 0; display: inline-flex; padding: 5px;
    background: none; border: none; border-radius: 6px;
    color: var(--faint); cursor: pointer;
  }
  .drevoke:hover { color: var(--err); background: var(--s3); }
  .drevoke:disabled { opacity: .4; cursor: progress; }
  .dorphan {
    margin: -4px 0 6px 15px; font-size: 10.5px; color: var(--warn);
  }
  .err.small { font-size: 11.5px; margin: 0 0 8px; }

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
