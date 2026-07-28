<script>
  // segmented control — the TP-7 mode selector (mechanical toggle bank)
  // options: [{value, label}]  ·  value bound
  let { options = [], value = $bindable(), onchange } = $props();
  function pick(v) { value = v; onchange?.(v); }
</script>

<div class="seg" role="tablist">
  {#each options as o}
    <button
      role="tab"
      class="segbtn"
      class:on={value === o.value}
      aria-selected={value === o.value}
      onclick={() => pick(o.value)}
    >{o.label}</button>
  {/each}
</div>

<style>
  .seg {
    display: inline-flex;
    border: 1px solid var(--line2);
    border-radius: var(--r);
    background: var(--s2);
    padding: 2px;
    gap: 2px;
  }
  .segbtn {
    font-family: var(--font-mono); font-size: 9.5px; font-weight: 500;
    letter-spacing: .1em; text-transform: uppercase;
    padding: 4px 11px;
    border: none; border-radius: var(--r-sm);
    background: transparent; color: var(--dim);
    cursor: pointer; white-space: nowrap;
    transition: color .1s, background .1s;
  }
  .segbtn:hover { color: var(--text); }
  .segbtn.on { color: var(--accent-ink); background: var(--accent); }
</style>
