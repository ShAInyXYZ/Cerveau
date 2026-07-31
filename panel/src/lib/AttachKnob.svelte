<script>
  import { Paperclip, Image, Mic, Video, Check, X } from 'lucide-svelte';
  import { tooltip } from '../kit/tooltip.js';

  // Circular attach control mirroring the ModeKnob, on the right of the chat bar.
  // Hover ~1s to reveal a compact capability grid: each input type as an icon with
  // a green (accepts) / red (rejects) status, read live from the model's modalities.
  let { modalities = null, onAttach } = $props();

  const TYPES = [
    { key: 'vision', label: 'Image', icon: Image },
    { key: 'audio',  label: 'Audio', icon: Mic },
    { key: 'video',  label: 'Video', icon: Video }
  ];

  const accepts = (k) => modalities?.[k] === true;
  const anyAccepted = $derived(TYPES.some((t) => accepts(t.key)));

  let showBar = $state(false);
  let timer = null;
  function enter() { timer = setTimeout(() => (showBar = true), 1000); } // 1s dwell
  function leave() { showBar = false; if (timer) { clearTimeout(timer); timer = null; } }
  function pick(t) { if (!accepts(t.key)) return; onAttach?.(t.key); leave(); }
</script>

<div class="attach" onmouseenter={enter} onmouseleave={leave} role="group">
  {#if showBar}
    <div class="cap-bar" role="menu">
      {#each TYPES as t}
        {@const ok = accepts(t.key)}
        {@const Icon = t.icon}
        <button class="cap" class:ok disabled={!ok} onclick={() => pick(t)}
          role="menuitem" aria-label="{t.label}: {ok ? 'accepted' : 'not accepted'}">
          <span class="cap-ico"><Icon size={17} strokeWidth={1.9} /></span>
          <span class="badge" class:on={ok}>
            {#if ok}<Check size={9} strokeWidth={3.5} />{:else}<X size={9} strokeWidth={3.5} />{/if}
          </span>
        </button>
      {/each}
    </div>
  {/if}

  <button
    class="knobbtn"
    class:live={anyAccepted}
    onclick={() => anyAccepted && onAttach?.(TYPES.find((t) => accepts(t.key))?.key)}
    disabled={!anyAccepted}
    aria-label="attach"
    use:tooltip={anyAccepted ? 'attach' : 'text-only model'}
  >
    <svg viewBox="0 0 54 54" class="knob">
      <circle cx="27" cy="27" r="26.5" fill="var(--s2)" stroke="var(--line2)" stroke-width="1" />
      <circle cx="27" cy="27" r="14" fill="var(--s3)" stroke="var(--line2)" stroke-width="1" />
    </svg>
    <span class="ico"><Paperclip size={16} strokeWidth={2} /></span>
    {#if anyAccepted}<span class="live-dot"></span>{/if}
  </button>
</div>

<style>
  .attach { position: relative; display: inline-flex; align-self: center; }

  .knobbtn {
    position: relative;
    width: 54px; height: 54px; padding: 0; border: none;
    background: transparent; cursor: pointer; flex-shrink: 0;
    display: inline-flex; align-items: center; justify-content: center;
  }
  .knobbtn:disabled { cursor: default; }
  .knob { width: 54px; height: 54px; display: block; }
  .ico {
    position: absolute; inset: 0; display: flex; align-items: center; justify-content: center;
    color: var(--dim); transition: color .12s;
  }
  .knobbtn.live .ico { color: var(--muted); }
  .knobbtn.live:hover .ico { color: var(--accent); }
  .knobbtn:disabled .ico { color: var(--faint); }

  .live-dot {
    position: absolute; top: 12px; right: 13px;
    width: 5px; height: 5px; border-radius: 50%;
    background: var(--ok); box-shadow: 0 0 6px -1px var(--ok);
  }

  /* capability grid — a compact row of icon tiles, each with a corner status badge */
  .cap-bar {
    position: absolute; bottom: calc(100% + 10px); right: 0;
    display: flex; gap: 6px; z-index: 40;
    background: var(--surface); border-radius: 12px;
    box-shadow: 0 0 0 1px var(--line2), 0 1px 0 0 var(--lift) inset;
    padding: 8px; animation: rise .16s cubic-bezier(.16,1,.3,1);
  }
  .cap {
    position: relative;
    width: 44px; height: 44px; border: none; border-radius: 10px;
    background: color-mix(in srgb, #fff 3%, transparent);
    box-shadow: inset 0 0 0 1px var(--line2);
    display: inline-flex; align-items: center; justify-content: center;
    color: var(--dim); cursor: default;
  }
  .cap.ok { color: var(--muted); cursor: pointer; }
  .cap.ok:hover { background: color-mix(in srgb, var(--ok) 12%, transparent); color: var(--text); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--ok) 40%, transparent); }
  .cap:disabled { opacity: .55; }
  .cap-ico { display: inline-flex; }

  /* corner status badge: green check / red x */
  .badge {
    position: absolute; top: -4px; right: -4px;
    width: 15px; height: 15px; border-radius: 50%;
    display: inline-flex; align-items: center; justify-content: center;
    background: var(--err); color: #fff;
    box-shadow: 0 0 0 2px var(--s1);
  }
  .badge.on { background: var(--ok); }

  @keyframes rise { from { opacity: 0; transform: translateY(4px); } to { opacity: 1; transform: none; } }
  @media (prefers-reduced-motion: reduce) { .cap-bar { animation: none; } }
</style>
