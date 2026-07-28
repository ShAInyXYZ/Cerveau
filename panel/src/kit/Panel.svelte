<script>
  // A framed module: header strip with a printed label + optional tag/actions,
  // then content. The core layout unit of the app.
  let { label = '', tag = '', flush = false, children, actions } = $props();
</script>

<section class="panel">
  {#if label || actions}
    <header class="phead">
      <span class="label">{label}</span>
      {#if tag}<span class="ptag">{tag}</span>{/if}
      <div class="pspace"></div>
      {@render actions?.()}
    </header>
  {/if}
  <div class="pbody" class:flush>
    {@render children?.()}
  </div>
</section>

<style>
  .panel {
    display: flex; flex-direction: column; min-height: 0;
    background: var(--s1);
    border: 1px solid var(--line);
    border-radius: var(--r-lg);
    overflow: hidden;
  }
  .phead {
    display: flex; align-items: center; gap: 8px;
    height: 34px; flex-shrink: 0;
    padding: 0 12px;
    border-bottom: 1px solid var(--line);
    background: var(--s1);
  }
  .ptag {
    font-family: var(--font-mono); font-size: 9px; letter-spacing: .04em;
    color: var(--dim); padding: 1px 6px; border: 1px solid var(--line2); border-radius: var(--r-sm);
  }
  .pspace { flex: 1; }
  .pbody { flex: 1; min-height: 0; overflow: auto; padding: var(--pad); }
  .pbody.flush { padding: 0; }
</style>
