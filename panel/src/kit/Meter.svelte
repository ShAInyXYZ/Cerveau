<script>
  // context-fill gauge — a segmented industrial bar (like a level meter)
  let { value = 0, max = 1, zone = 'green', segments = 16 } = $props();
  const pct = $derived(max > 0 ? Math.min(1, value / max) : 0);
  const lit = $derived(Math.round(pct * segments));
  const tone = $derived(zone === 'red' ? 'err' : zone === 'yellow' ? 'warn' : 'ok');
</script>

<div class="meter" use:tooltip={`${value} / ${max} tokens · ${zone}`}>
  {#each Array(segments) as _, i}
    <span class="seg {i < lit ? tone : ''}"></span>
  {/each}
</div>

<style>
  .meter { display: inline-flex; gap: 2px; align-items: center; }
  .seg {
    width: 3px; height: 11px; border-radius: 0;
    background: var(--line2);
    transition: background .2s;
  }
  .seg.ok { background: var(--ok); }
  .seg.warn { background: var(--warn); }
  .seg.err { background: var(--err); }
</style>
