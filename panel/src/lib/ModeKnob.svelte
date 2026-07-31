<script>
  import { DropdownMenu } from 'bits-ui';
  import { Check } from 'lucide-svelte';
  import { MODES, modeMeta } from './modes.js';
  import { tooltip } from '../kit/tooltip.js';

  let { mode = $bindable('discussion') } = $props();

  const idx = $derived(Math.max(0, MODES.findIndex((m) => m.value === mode)));
  const current = $derived(modeMeta(mode));

  // three detents -> indicator angle (deg). left / centre / right.
  const ANGLES = [-40, 0, 40];

  let menuOpen = $state(false);

  // ---- drag / scroll to rotate (up = clockwise = advance) ----
  let dragging = $state(false);
  let startY = 0, startIdx = 0;
  function setIdx(i) { mode = MODES[Math.max(0, Math.min(MODES.length - 1, i))].value; }
  function down(e) { dragging = true; startY = e.clientY; startIdx = idx; e.currentTarget.setPointerCapture?.(e.pointerId); }
  function move(e) { if (dragging) setIdx(startIdx + Math.round((startY - e.clientY) / 26)); }
  function up(e) { dragging = false; e.currentTarget.releasePointerCapture?.(e.pointerId); }
  function wheel(e) { e.preventDefault(); setIdx(idx + (e.deltaY < 0 ? 1 : -1)); }

  // geometry (viewBox 0..56, centre 28,28) — roomy margins
  const C = 27, TICK_R = 21, DIAL_R = 14;
  function tx(a) { return C + TICK_R * Math.sin(a * Math.PI / 180); }
  function ty(a) { return C - TICK_R * Math.cos(a * Math.PI / 180); }
</script>

<DropdownMenu.Root bind:open={menuOpen}>
  <DropdownMenu.Trigger>
    {#snippet child({ props })}
      <button
        {...props}
        class="knobbtn" class:dragging
        use:tooltip={`${current.title} — drag to switch, click for menu`}
        aria-label="mode: {current.title}"
        onpointerdown={down} onpointermove={move} onpointerup={up} onwheel={wheel}
      >
        <svg viewBox="0 0 54 54" class="knob">
          <!-- outer ring (was a CSS border; now in-SVG so the icon shares the exact centre) -->
          <circle class="ring" cx={C} cy={C} r="26.5" fill="var(--s2)" stroke="var(--line2)" stroke-width="1" />
          <!-- fixed detent dots — one per mode, in its colour; active is lit -->
          {#each MODES as m, i}
            {@const a = ANGLES[i]}
            <circle
              cx={tx(a)} cy={ty(a)} r={i === idx ? 2.4 : 1.6}
              fill={m.color}
              opacity={i === idx ? 1 : 0.32}
            />
          {/each}

          <!-- dial body (offset in from the ticks) -->
          <circle class="dial" cx={C} cy={C} r={DIAL_R} fill="var(--s3)" stroke="var(--line2)" stroke-width="1" />
          <circle cx={C} cy={C} r={DIAL_R} fill="url(#kg)" />

          <!-- mode glyph — hand-drawn about origin, mass balanced so (0,0) is the optical centre -->
          <g transform="translate({C} {C})" fill="none" stroke={current.color}
             stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            {#if mode === 'discussion'}
              <!-- speech bubble (rounded rect + short tail), balanced about origin -->
              <rect x="-7" y="-6" width="14" height="10" rx="3" />
              <path d="M -3.5 4 L -3.5 6.5 L 0 4" fill={current.color} stroke="none" />
              <circle cx="-3" cy="-1" r="0.9" fill={current.color} stroke="none" />
              <circle cx="0" cy="-1" r="0.9" fill={current.color} stroke="none" />
              <circle cx="3" cy="-1" r="0.9" fill={current.color} stroke="none" />
            {:else if mode === 'brainstorming'}
              <!-- lightbulb, balanced about origin -->
              <path d="M -4.5 -1 A 4.5 4.5 0 1 1 4.5 -1 C 4.5 1 3 2 2.5 3.5 H -2.5 C -3 2 -4.5 1 -4.5 -1 Z" />
              <path d="M -2.3 5.2 H 2.3" />
              <path d="M -1.5 7 H 1.5" />
            {:else}
              <!-- steering wheel: outer rim, hub, three spokes -->
              <circle cx="0" cy="0" r="7" />
              <circle cx="0" cy="0" r="1.8" fill={current.color} stroke="none" />
              <path d="M 0 1.8 V 7" />
              <path d="M -1.6 -0.9 L -6 -3.6" />
              <path d="M 1.6 -0.9 L 6 -3.6" />
            {/if}
          </g>

          <defs>
            <radialGradient id="kg" cx="50%" cy="34%" r="70%">
              <stop offset="0%" stop-color="var(--s3)" />
              <stop offset="100%" stop-color="var(--s1)" />
            </radialGradient>
          </defs>
        </svg>
      </button>
    {/snippet}
  </DropdownMenu.Trigger>

  <DropdownMenu.Portal>
    <DropdownMenu.Content class="menu" sideOffset={12} align="start" side="top">
      <div class="mhead label">MODE</div>
      {#each MODES as m}
        {@const Icon = m.icon}
        <DropdownMenu.Item class="item" onSelect={() => (mode = m.value)}>
          <div class="iicon" class:on={m.value === mode}
            style={m.value === mode ? `color:${m.color}; border-color:color-mix(in srgb, ${m.color} 45%, var(--line2)); background:color-mix(in srgb, ${m.color} 12%, transparent)` : ''}>
            <Icon size={15} />
          </div>
          <div class="itext">
            <div class="ititle">{m.title}</div>
            <div class="idesc">{m.desc}</div>
          </div>
          {#if m.value === mode}<Check size={14} color={m.color} />{/if}
        </DropdownMenu.Item>
      {/each}
    </DropdownMenu.Content>
  </DropdownMenu.Portal>
</DropdownMenu.Root>

<style>
  .knobbtn {
    position: relative;
    width: 54px; height: 54px; flex-shrink: 0;
    padding: 0; border: none; background: transparent;
    border-radius: 50%;
    filter: drop-shadow(0 1px 0 var(--lift));
    cursor: grab; touch-action: none;
    display: block;
    transition: filter .14s;
  }
  .knobbtn:hover { filter: drop-shadow(0 1px 0 var(--lift-strong)); }
  .knobbtn.dragging { cursor: grabbing; }
  .knob { display: block; width: 54px; height: 54px; }
  .knobbtn:hover .knob .ring { stroke: var(--faint); }
  .knobbtn.dragging .knob .ring { stroke: var(--accent-line); }


  :global(.menu) {
    z-index: 70; min-width: 264px;
    background: var(--surface); border-radius: 10px;
    padding: 6px; box-shadow: var(--elev-2);
  }
  .mhead { padding: 6px 8px 8px; }
  :global(.menu .item) {
    display: flex; align-items: center; gap: 11px;
    padding: 9px 8px; border-radius: var(--r); cursor: pointer; outline: none; color: var(--muted);
  }
  :global(.menu .item[data-highlighted]) { background: var(--s2); }
  .iicon {
    display: flex; align-items: center; justify-content: center;
    width: 30px; height: 30px; flex-shrink: 0;
    border: 1px solid var(--line2); border-radius: var(--r);
    background: var(--s2); color: var(--dim);
  }
  .iicon.on { color: var(--accent); border-color: var(--accent-line); background: var(--accent-soft); }
  .itext { flex: 1; min-width: 0; }
  .ititle { font-size: 12.5px; font-weight: 600; color: var(--text); }
  .idesc { font-size: 10.5px; color: var(--dim); margin-top: 1px; }
</style>
