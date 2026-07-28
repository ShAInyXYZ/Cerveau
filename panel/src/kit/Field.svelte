<script>
  // industrial text field — hairline box, accent focus, mono placeholder tone
  let {
    value = $bindable(''),
    placeholder = '',
    multiline = false,
    rows = 1,
    disabled = false,
    onenter,     // fired on Enter (not Shift+Enter)
    onkeydown
  } = $props();

  function key(e) {
    onkeydown?.(e);
    if (e.key === 'Enter' && !e.shiftKey && onenter) {
      e.preventDefault();
      onenter();
    }
  }
</script>

{#if multiline}
  <textarea class="field" bind:value {placeholder} {rows} {disabled} onkeydown={key}></textarea>
{:else}
  <input class="field" bind:value {placeholder} {disabled} onkeydown={key} />
{/if}

<style>
  .field {
    width: 100%;
    background: var(--s2);
    border: 1px solid var(--line2);
    border-radius: var(--r);
    color: var(--text);
    font-family: var(--font-sans);
    font-size: 13px; line-height: 1.5;
    padding: 9px 11px;
    outline: none;
    resize: none;
    transition: border-color .12s, background .12s;
  }
  .field:focus { border-color: var(--accent-line); background: var(--bg); }
  .field::placeholder { color: var(--faint); }
  .field:disabled { opacity: .5; }
</style>
