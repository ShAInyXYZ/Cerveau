<script module lang="ts">
  // nvme: an M.2 stick · sata: a 2.5" SSD · hdd: a 3.5" spinning drive
  export type ArtKind = 'gpu' | 'cpu' | 'ram' | 'nvme' | 'sata' | 'hdd' | 'nas';
  // idle: nothing runs here · on: something does · accent: the layout being
  // edited targets it. The accent is kept for that one meaning.
  export type ArtTone = 'idle' | 'on' | 'accent';
  // The drawings have different shapes. These are the widths at which each sits
  // in the same 118px-tall box on a card — they belong with the drawings.
  export const ART_WIDTH: Record<ArtKind, number> = { gpu: 196, cpu: 92, ram: 196, nvme: 196, sata: 136, hdd: 136, nas: 72 };
</script>

<script lang="ts">
  // The Rig's hardware illustrations, inlined so they take the panel's tokens —
  // the same way EngineMark inlines the engine logos. The files are generated
  // by scripts/make-rig-art.mjs and carry no colour and no timing of their own:
  //   b body · p raised part · v void · d detail line · m marker
  //   fan · platter · seek · lit   what moves, and only while there is load
  import gpu from './gpu.svg?raw';
  import cpu from './cpu.svg?raw';
  import ram from './ram.svg?raw';
  import nvme from './nvme.svg?raw';
  import sata from './sata.svg?raw';
  import hdd from './hdd.svg?raw';
  import nas from './nas.svg?raw';

  let { kind, width = 200, load = 0, tone = 'idle', label = '' }: {
    kind: ArtKind; width?: number; load?: number; tone?: ArtTone; label?: string;
  } = $props();

  const ART: Record<ArtKind, string> = { gpu, cpu, ram, nvme, sata, hdd, nas };
  // One number drives every illustration. At 0 nothing moves — a parked card's
  // fans are still. `--dur` is a GPU fan's full turn: 3 s barely loaded, 0.6 s
  // flat out. Every other motion is a fixed multiple of it, so the whole rig
  // speeds up and slows down together.
  //
  // Load arrives from a live meter and wobbles by a percent every poll; a CSS
  // animation jumps when its duration changes. So speed moves in five steps.
  const live = $derived(load > 0);
  const level = $derived(Math.ceil(Math.min(1, load) * 5) / 5);
  const dur = $derived(`${(3 - 2.4 * level).toFixed(2)}s`);
</script>

<span class="rig-art {tone}" class:live style="--w:{width}px; --dur:{dur}"
  role={label ? 'img' : undefined} aria-label={label || undefined} aria-hidden={label ? undefined : 'true'}>
  {@html ART[kind]}
</span>

<style>
  .rig-art {
    display: inline-block; width: var(--w); line-height: 0; flex-shrink: 0;
    --art-fill: var(--s2); --art-part: var(--s3); --art-void: var(--bg);
    --art-line: var(--dim); --art-detail: var(--faint); --art-lit: var(--muted);
  }
  .rig-art.on     { --art-line: var(--muted);  --art-detail: var(--dim);         --art-lit: var(--text); }
  .rig-art.accent { --art-line: var(--accent); --art-detail: var(--accent-line); --art-lit: var(--accent); }

  .rig-art :global(svg) { width: 100%; height: auto; display: block; overflow: visible; }
  /* hairlines stay one pixel whatever size the card draws the art at */
  .rig-art :global(svg *) { vector-effect: non-scaling-stroke; }
  .rig-art :global(.b) { fill: var(--art-fill); stroke: var(--art-line); }
  .rig-art :global(.p) { fill: var(--art-part); stroke: var(--art-line); }
  .rig-art :global(.v) { fill: var(--art-void); stroke: var(--art-line); }
  .rig-art :global(.d) { fill: none; stroke: var(--art-detail); }
  .rig-art :global(.m) { fill: var(--art-line); stroke: none; }
  /* an overlay on top of a part: invisible until its turn in the cycle */
  .rig-art :global(.lit) { fill: var(--art-lit); stroke: none; opacity: 0; }

  /* Everything that turns sits at its own origin, so it turns in place.
     tokens.css stops all of this under prefers-reduced-motion. */
  .rig-art.live :global(.fan)     { animation: rig-turn var(--dur) linear infinite; }
  .rig-art.live :global(.fan.ccw) { animation-direction: reverse; }  /* the pair counter-rotates, as on the site */
  .rig-art.live :global(.platter) { animation: rig-turn calc(var(--dur) * 1.4) linear infinite; }
  /* fast between tracks, still on them */
  .rig-art.live :global(.seek)    { animation: rig-seek calc(var(--dur) * 3.2) cubic-bezier(.7, 0, .2, 1) infinite; }

  /* --p places each overlay in the cycle; a negative delay starts it mid-cycle
     so a chase is already spread out on the first frame */
  .rig-art.live :global(.lit) {
    animation: rig-lit calc(var(--dur) * 1.6) linear infinite;
    animation-delay: calc(var(--p, 0) * var(--dur) * -1.6);
  }
  .rig-art.live :global(.lit.soft)   { animation-name: rig-lit-soft; }
  .rig-art.live :global(.lit.blink)  {
    animation: rig-blink calc(var(--dur) * 2.3) steps(1, end) infinite;
    animation-delay: calc(var(--p, 0) * var(--dur) * -2.3);
  }
  .rig-art.live :global(.lit.steady) { animation: none; opacity: 1; }

  @keyframes rig-turn { to { transform: rotate(360deg); } }
  @keyframes rig-seek {
    0%, 100% { transform: rotate(40deg); }
    14% { transform: rotate(56deg); } 30% { transform: rotate(44deg); }
    52% { transform: rotate(58deg); } 66% { transform: rotate(36deg); }
    82% { transform: rotate(50deg); }
  }
  /* a small mark lights fully; a large face only lifts, or it would flash */
  @keyframes rig-lit      { 0%, 30%, 100% { opacity: 0; } 6%, 14% { opacity: 1; } }
  @keyframes rig-lit-soft { 0%, 44%, 100% { opacity: 0; } 10%, 24% { opacity: .22; } }
  @keyframes rig-blink {
    0% { opacity: 1; } 8% { opacity: 0; } 16% { opacity: 1; } 22% { opacity: 0; }
    45% { opacity: 1; } 52% { opacity: 0; } 58% { opacity: 1; } 70%, 100% { opacity: 0; }
  }
</style>
