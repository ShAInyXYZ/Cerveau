<script>
  // TP-7 industrial button: square, mono-uppercase, mechanical press.
  // variants: default | primary | ghost | danger | quiet
  let {
    variant = 'default',
    size = 'md',        // sm | md
    disabled = false,
    title = '',
    active = false,
    onclick,
    children
  } = $props();
</script>

<button class="btn {variant} {size}" class:active {disabled} {title} {onclick}>
  {@render children?.()}
</button>

<style>
  .btn {
    display: inline-flex; align-items: center; justify-content: center; gap: 6px;
    font-family: var(--font-mono);
    font-weight: 500;
    letter-spacing: .1em;
    text-transform: uppercase;
    border: 1px solid var(--line2);
    border-radius: var(--r);
    background: var(--s2);
    color: var(--muted);
    cursor: pointer;
    white-space: nowrap;
    transition: background .1s, border-color .1s, color .1s, transform .04s;
    user-select: none;
  }
  .md { padding: 7px 13px; font-size: 10px; }
  .sm { padding: 5px 9px; font-size: 9px; }

  .btn:hover:not(:disabled) { color: var(--text); border-color: var(--faint); background: var(--s3); }
  .btn:active:not(:disabled) { transform: translateY(1px); }
  .btn:disabled { opacity: .4; cursor: default; }

  .btn.active { color: var(--accent); border-color: var(--accent-line); background: var(--accent-soft); }

  .primary { background: var(--accent); color: var(--accent-ink); border-color: var(--accent); }
  .primary:hover:not(:disabled) { background: var(--accent); color: var(--accent-ink); filter: brightness(1.08); border-color: var(--accent); }

  .ghost { background: transparent; border-color: transparent; color: var(--dim); }
  .ghost:hover:not(:disabled) { color: var(--text); background: var(--s2); border-color: transparent; }

  .quiet { background: transparent; border-color: transparent; color: var(--dim); padding-left: 4px; padding-right: 4px; }
  .quiet:hover:not(:disabled) { color: var(--text); background: transparent; }

  .danger:hover:not(:disabled) { color: var(--err); border-color: var(--err); background: transparent; }
</style>
