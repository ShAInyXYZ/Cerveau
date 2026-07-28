<script>
  // Themed select built on bits-ui DropdownMenu — replaces the raw browser
  // <select> so dropdowns match the app (ModeKnob uses the same primitive).
  import { DropdownMenu } from 'bits-ui';
  import { Check, ChevronDown } from 'lucide-svelte';

  let {
    value = $bindable(),
    options = [],        // [{ value, label }]
    placeholder = 'Select…',
    align = 'start',
    side = 'bottom'
  } = $props();

  let open = $state(false);
  const current = $derived(options.find((o) => o.value === value));
</script>

<DropdownMenu.Root bind:open>
  <DropdownMenu.Trigger class="sel-trigger">
    <span class="sel-val">{current?.label ?? placeholder}</span>
    <ChevronDown class="sel-chev {open ? 'open' : ''}" size={13} />
  </DropdownMenu.Trigger>

  <DropdownMenu.Portal>
    <DropdownMenu.Content class="sel-menu" sideOffset={6} {align} {side}>
      {#each options as o}
        <DropdownMenu.Item class="sel-item" onSelect={() => (value = o.value)}>
          <span class="sel-item-label">{o.label}</span>
          {#if o.value === value}<Check size={13} class="sel-check" />{/if}
        </DropdownMenu.Item>
      {/each}
    </DropdownMenu.Content>
  </DropdownMenu.Portal>
</DropdownMenu.Root>

<style>
  :global(.sel-trigger) {
    display: inline-flex; align-items: center; gap: 8px;
    font-family: var(--font-mono); font-size: 11.5px; color: var(--muted);
    background: var(--s2); border: 1px solid var(--line2); border-radius: 8px;
    padding: 5px 8px 5px 10px; cursor: pointer;
    transition: color .12s, border-color .12s, background .12s;
  }
  :global(.sel-trigger:hover) { color: var(--text); border-color: var(--faint); background: var(--s3); }
  :global(.sel-trigger[data-state="open"]) { color: var(--text); border-color: var(--accent-line); }
  .sel-val { max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  :global(.sel-chev) { color: var(--faint); transition: transform .15s; flex-shrink: 0; }
  :global(.sel-chev.open) { transform: rotate(180deg); }

  :global(.sel-menu) {
    z-index: 70; min-width: 200px; max-height: 320px; overflow-y: auto;
    background: var(--surface); border-radius: 10px;
    padding: 5px; box-shadow: var(--elev-2);
  }
  :global(.sel-item) {
    display: flex; align-items: center; justify-content: space-between; gap: 10px;
    padding: 7px 10px; border-radius: 7px; cursor: pointer;
    font-size: 12.5px; color: var(--muted); outline: none;
  }
  :global(.sel-item[data-highlighted]) { background: color-mix(in srgb,#fff 5%,transparent); color: var(--text); }
  :global(.sel-item-label) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  :global(.sel-check) { color: var(--accent); flex-shrink: 0; }
</style>
