<script>
  // The engines' own marks, traced from their logos to single-path SVGs so they
  // take currentColor — grey when idle, accent when this Core is the live one.
  import llamacpp from './llamacpp.svg?raw';
  import vllm from './vllm.svg?raw';

  let { engine = '', size = 34 } = $props();
  const svg = $derived(/vllm/i.test(engine) ? vllm : llamacpp);
</script>

<span class="mark" style="--s:{size}px">{@html svg}</span>

<style>
  .mark {
    width: var(--s); height: var(--s);
    display: inline-flex; align-items: center; justify-content: center;
    color: var(--dim); flex-shrink: 0;
    transition: color .14s;
  }
  .mark :global(svg) { width: 100%; height: 100%; display: block; }
  :global(.core.on) .mark { color: var(--accent); }
</style>
