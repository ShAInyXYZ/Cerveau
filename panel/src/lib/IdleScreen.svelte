<script>
  // The Idle screen — shown BEFORE the Core parks, not after.
  //
  // A park that happens silently is indistinguishable from a crash: the user
  // comes back to a cold machine and no explanation. So the screen appears
  // with time still on the clock (default 15 min), states plainly what will be
  // lost, and offers the two answers a person actually has — "still here" or
  // "go ahead".
  //
  // The cache line is the important one. Losing the KV cache sounds like losing
  // the conversation, and it is not: history and long-term memory are on disk.
  // Saying so is the difference between a calm screen and an alarming one.
  import { Moon, Coffee, Clock, Database } from 'lucide-svelte';
  import { api } from './api';
  import Button from '../kit/Button.svelte';

  let { status = null, onchange = () => {} } = $props();

  const s = $derived(status ?? { state: 'active', park_in_seconds: -1 });
  const warning = $derived(s.state === 'warning');
  const parked  = $derived(s.state === 'parked');
  const waking  = $derived(s.state === 'waking');

  // mm:ss, because a bare "600s" makes nobody reach for the mouse
  const countdown = $derived.by(() => {
    const t = s.park_in_seconds;
    if (t == null || t < 0) return '';
    const m = Math.floor(t / 60), sec = t % 60;
    return `${m}:${String(sec).padStart(2, '0')}`;
  });

  let busy = $state(false);

  async function stay(minutes) {
    busy = true;
    try { onchange(await api.idleStay(minutes)); } finally { busy = false; }
  }
  async function parkNow() {
    busy = true;
    try { onchange(await api.idleNow()); } finally { busy = false; }
  }
</script>

{#if warning || parked || waking}
  <div class="idle" class:parked class:waking>
    <div class="head">
      {#if waking}
        <span class="ico spin"><Coffee size={18} strokeWidth={2} /></span>
        <span class="kind">WAKING</span>
      {:else if parked}
        <span class="ico"><Moon size={18} strokeWidth={2} /></span>
        <span class="kind">IDLE</span>
      {:else}
        <span class="ico"><Clock size={18} strokeWidth={2} /></span>
        <span class="kind">GOING IDLE</span>
      {/if}
    </div>

    {#if waking}
      <h3>Waking the Core</h3>
      <p class="lede">
        Loading weights and rebuilding the cache. The first reply will take a
        little longer than usual — everything after it runs at full speed.
      </p>
      <div class="bar"><div class="fill"></div></div>

    {:else if parked}
      <h3>The Core is resting</h3>
      <p class="lede">
        Nothing is loaded, so the machine is drawing almost no power. Send a
        message and it wakes up on its own — give it a moment to load.
      </p>

    {:else}
      <h3>Going idle in <span class="clock">{countdown}</span></h3>
      <p class="lede">
        Nothing has run for a while, so the Core is about to unload and stop
        drawing power. Still working? Keep it awake.
      </p>

      <div class="note">
        <Database size={14} strokeWidth={2} />
        <p>
          <strong>Your conversation is safe.</strong> Sessions and long-term
          memory live on disk and come back untouched. What is lost is only the
          <em>active</em> cache — the warm context of the current operation —
          so the next message re-reads instead of resuming.
        </p>
      </div>

      <div class="acts">
        <Button variant="primary" onclick={() => stay(60)} disabled={busy}>Stay awake 1h</Button>
        <Button variant="ghost" onclick={() => stay(15)} disabled={busy}>15 more min</Button>
        <Button variant="quiet" onclick={parkNow} disabled={busy}>Go idle now</Button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .idle {
    --tone: var(--warn);
    /* a floating dialog now: bounded width, centred by .idlewrap in App,
       and lifted off the page rather than spanning it edge to edge */
    width: 100%;
    max-width: 460px;
    flex-shrink: 0;
    border-radius: 16px;
    padding: 20px 22px 18px;
    background: linear-gradient(180deg, color-mix(in srgb, var(--tone) 6%, var(--s1)) 0%, var(--s1) 46%);
    box-shadow:
      0 0 0 1px color-mix(in srgb, var(--tone) 22%, transparent),
      0 1px 0 0 color-mix(in srgb, #fff 4%, transparent) inset,
      0 -1px 0 0 var(--shade) inset,
      0 24px 60px -20px rgba(0, 0, 0, .7);
    animation: rise .2s cubic-bezier(.16,1,.3,1);
  }
  .idle.parked { --tone: var(--info); }
  .idle.waking { --tone: var(--accent); }

  .head { display: flex; align-items: center; gap: 8px; margin-bottom: 9px; }
  .ico { color: var(--tone); display: inline-flex; }
  .kind {
    font-family: var(--font-mono); font-size: 9px; font-weight: 600;
    letter-spacing: .18em; color: var(--tone); opacity: .9;
  }

  h3 { margin: 0 0 6px; font-size: 15px; font-weight: 560; color: var(--text); }
  .clock { font-family: var(--font-mono); color: var(--tone); }

  .lede { margin: 0; font-size: 13px; line-height: 1.55; color: var(--muted); }

  .note {
    display: flex; gap: 9px; align-items: flex-start;
    margin-top: 12px; padding: 10px 12px;
    border-radius: 8px; background: rgba(0,0,0,.22);
    box-shadow: inset 0 0 0 1px var(--ring);
  }
  .note :global(svg) { color: var(--info); flex-shrink: 0; margin-top: 2px; }
  .note p { margin: 0; font-size: 12px; line-height: 1.55; color: var(--dim); }
  .note strong { color: var(--muted); font-weight: 560; }
  .note em { color: var(--muted); font-style: normal; text-decoration: underline; text-decoration-color: var(--ring-strong); text-underline-offset: 2px; }

  .acts { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 13px; }

  /* waking — indeterminate sweep, since a cold start has no honest percentage */
  .bar {
    margin-top: 12px; height: 3px; border-radius: 2px;
    background: rgba(0,0,0,.3); overflow: hidden;
  }
  .fill {
    height: 100%; width: 38%; border-radius: 2px;
    background: var(--tone);
    animation: sweep 1.25s ease-in-out infinite;
  }
  .spin :global(svg) { animation: pulse 1.6s ease-in-out infinite; }

  @keyframes sweep { 0% { transform: translateX(-100%); } 100% { transform: translateX(360%); } }
  @keyframes pulse { 0%,100% { opacity: 1; } 50% { opacity: .45; } }
  @keyframes rise { from { opacity: 0; transform: translateY(5px) scale(.994); } to { opacity: 1; transform: none; } }

  @media (prefers-reduced-motion: reduce) {
    .idle { animation: none; }
    .fill, .spin :global(svg) { animation: none; }
    .fill { width: 100%; }
  }
</style>
