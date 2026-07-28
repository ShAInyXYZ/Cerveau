<script>
  import { DropdownMenu } from 'bits-ui';
  import { ChevronDown, Check } from 'lucide-svelte';
  import { MODES, modeMeta } from './modes.js';

  let { mode = $bindable('discussion') } = $props();
  const current = $derived(modeMeta(mode));
  let open = $state(false);
</script>

<DropdownMenu.Root bind:open>
  <DropdownMenu.Trigger class="trigger" aria-label="operating mode">
    {#snippet child({ props })}
      <button {...props} class="trigger" class:open>
        {#if current.icon}{@const Icon = current.icon}<Icon size={14} />{/if}
        <span class="tname">{current.title}</span>
        <ChevronDown size={13} class="chev" />
      </button>
    {/snippet}
  </DropdownMenu.Trigger>

  <DropdownMenu.Portal>
    <DropdownMenu.Content class="menu" sideOffset={6} align="start">
      <div class="mhead label">MODE</div>
      {#each MODES as m}
        {@const Icon = m.icon}
        <DropdownMenu.Item class="item" onSelect={() => (mode = m.value)}>
          <div class="iicon" class:on={m.value === mode}><Icon size={15} /></div>
          <div class="itext">
            <div class="ititle">{m.title}</div>
            <div class="idesc">{m.desc}</div>
          </div>
          {#if m.value === mode}<Check size={14} class="icheck" />{/if}
        </DropdownMenu.Item>
      {/each}
    </DropdownMenu.Content>
  </DropdownMenu.Portal>
</DropdownMenu.Root>

<style>
  :global(.trigger) {
    display: inline-flex; align-items: center; gap: 7px;
    height: 30px; padding: 0 9px 0 11px;
    border: 1px solid var(--line2); border-radius: var(--r);
    background: var(--s2); color: var(--text);
    font-family: var(--font-mono); font-size: 11px; font-weight: 500;
    letter-spacing: .06em; cursor: pointer;
    transition: border-color .1s, background .1s;
  }
  :global(.trigger:hover) { border-color: var(--faint); background: var(--s3); }
  :global(.trigger.open) { border-color: var(--accent-line); }
  :global(.trigger .chev) { color: var(--dim); margin-left: 1px; }
  .tname { text-transform: uppercase; }

  :global(.menu) {
    z-index: 60;
    min-width: 268px;
    background: var(--s1);
    border: 1px solid var(--line2);
    border-radius: var(--r-lg);
    padding: 6px;
    box-shadow: 0 8px 28px -12px rgba(0,0,0,.7);
    animation: rise .13s cubic-bezier(.16,1,.3,1);
  }
  .mhead { padding: 6px 8px 8px; }

  :global(.menu .item) {
    display: flex; align-items: center; gap: 11px;
    padding: 9px 8px; border-radius: var(--r);
    cursor: pointer; outline: none;
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
  :global(.menu .item .icheck) { color: var(--accent); }

  @keyframes rise { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: none; } }
</style>
