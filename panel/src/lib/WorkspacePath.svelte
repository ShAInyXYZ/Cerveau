<script lang="ts">
  import { jpost } from './api';
  import { tooltip } from '../kit/tooltip.js';
  import { FolderCog } from 'lucide-svelte';
  import WorkspacePicker from './WorkspacePicker.svelte';

  let { workspace = '', onChanged } = $props<{
    workspace?: string;
    onChanged?: (path: string) => void;
  }>();
  let picking = $state(false);
  let browsing = $state(false);

  // The native dialog opens on the MACHINE — invisible and unclickable from a
  // phone — so anything that isn't a desktop-width screen uses the in-panel
  // browser instead. Both reach the same place.
  const narrow = typeof matchMedia !== 'undefined'
    && matchMedia('(max-width: 900px)').matches;

  async function pick() {
    if (picking) return;
    if (narrow) {
      // dismiss the hover tooltip before the sheet covers the button,
      // otherwise it stays painted on top of the dialog
      (document.activeElement as HTMLElement | null)?.blur?.();
      browsing = true;
      return;
    }
    picking = true;
    const res = await jpost<{ workspace?: string }>('/api/config/pick-workspace', {});
    picking = false;
    if (res?.workspace) onChanged?.(res.workspace);
  }

  async function pickPath(path: string) {
    const res = await jpost<{ ok?: string }>('/api/config/workspace', { path });
    if (res?.ok) onChanged?.(path);
  }
</script>

<button class="wpbtn" onclick={pick} disabled={picking}
  use:tooltip={"change workspace — opens a folder picker"}>
  <FolderCog size={11} />
  <span class="label">WS</span>
  <span class="wppath mono">{picking ? 'choosing…' : (workspace || '—')}</span>
</button>

<WorkspacePicker bind:open={browsing} current={workspace} onPick={pickPath} />

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
