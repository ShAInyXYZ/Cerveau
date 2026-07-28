<script>
  // square icon button — the TP-7 module control
  import { tooltip } from './tooltip.js';
  let { title = '', active = false, disabled = false, onclick, children } = $props();
</script>

<button class="ib" class:active {disabled} {onclick} aria-label={title}
  use:tooltip={{ text: title, placement: 'bottom' }}>
  {@render children?.()}
</button>

<style>
  .ib {
    display: inline-flex; align-items: center; justify-content: center;
    width: 30px; height: 30px;
    border: 1px solid var(--line2);
    border-radius: var(--r);
    background: var(--s2);
    color: var(--dim);
    cursor: pointer;
    transition: color .1s, border-color .1s, background .1s, transform .04s;
  }
  .ib:hover:not(:disabled) { color: var(--text); border-color: var(--faint); background: var(--s3); }
  .ib:active:not(:disabled) { transform: translateY(1px); }
  .ib:disabled { opacity: .4; cursor: default; }
  .ib.active { color: var(--accent); border-color: var(--accent-line); background: var(--accent-soft); }
</style>
