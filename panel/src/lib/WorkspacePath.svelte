<script>
  import { jpost } from './api';
  import { tooltip } from '../kit/tooltip.js';
  import { FolderCog } from 'lucide-svelte';

  let { workspace = '', onChanged } = $props();
  let picking = $state(false);

  async function pick() {
    if (picking) return;
    picking = true;
    // server opens the OS native folder dialog (crv runs locally)
    const res = await jpost('/api/config/pick-workspace', {});
    picking = false;
    if (res?.workspace) onChanged?.(res.workspace);
  }
</script>

<button class="wpbtn" onclick={pick} disabled={picking}
  use:tooltip={"change workspace — opens a folder picker"}>
  <FolderCog size={11} />
  <span class="label">WS</span>
  <span class="wppath mono">{picking ? 'choosing…' : (workspace || '—')}</span>
</button>

<style>
  .wpbtn {
    display: inline-flex; align-items: center; gap: 7px;
    background: transparent; border: none; border-radius: 8px;
    padding: 5px 11px 5px 10px; cursor: pointer; max-width: 100%; min-width: 0;
    color: var(--faint);
    transition: color .1s, background .1s;
  }
  .wpbtn:hover:not(:disabled) { color: var(--muted); background: color-mix(in srgb, #fff 4%, transparent); }
  .wpbtn:disabled { cursor: default; opacity: .7; }
  .wppath {
    font-size: 11px; color: var(--dim);
    max-width: 320px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    direction: rtl; text-align: right;
  }
  .wpbtn:hover:not(:disabled) .wppath { color: var(--muted); }
</style>
