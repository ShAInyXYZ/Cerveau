<script>
  import { Handle, Position } from '@xyflow/svelte';
  import { tooltip } from '../kit/tooltip.js';
  let { data } = $props();
  // data: { type, content, category, confidence, ts, session_id, state }
  function fmtDate(ts) {
    if (!ts) return '';
    const d = new Date(ts * 1000);
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) +
      ' · ' + d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  }
</script>

<div class="mn {data.type} {data.state}" use:tooltip={data.content}>
  <Handle type="target" position={Position.Top} style="opacity:0" />
  <div class="mhead">
    <span class="dot"></span>
    <span class="mt mono">{data.type}</span>
    {#if data.category}<span class="cat mono">{data.category}</span>{/if}
    {#if data.confidence}<span class="conf mono">{Math.round(data.confidence * 100)}</span>{/if}
  </div>
  <div class="mbody">{data.content}</div>
  {#if data.ts}<div class="mdate mono">{fmtDate(data.ts)}</div>{/if}
  <Handle type="source" position={Position.Bottom} style="opacity:0" />
</div>

<style>
  .mn {
    --c: var(--faint);
    width: 190px;
    background: linear-gradient(180deg, color-mix(in srgb, var(--c) 5%, var(--s2)) 0%, var(--s2) 50%);
    border-radius: 9px;
    box-shadow: 0 0 0 1px color-mix(in srgb, var(--c) 22%, transparent), 0 1px 0 0 var(--lift) inset;
    padding: 8px 11px;
    transition: opacity .2s, box-shadow .2s, transform .2s;
  }
  .mn.semantic { --c: var(--semantic); }
  .mn.episodic { --c: var(--episodic); }

  .mn.dim   { opacity: .2; }
  .mn.match { box-shadow: 0 0 0 1.5px var(--c), 0 0 22px -4px var(--c); }
  .mn.focus { transform: scale(1.06); box-shadow: 0 0 0 2px var(--c), 0 0 30px -2px var(--c); z-index: 5; }

  .mhead { display: flex; align-items: center; gap: 7px; margin-bottom: 5px; }
  .dot { width: 6px; height: 6px; border-radius: 2px; background: var(--c); flex-shrink: 0; }
  .mt { font-size: 8.5px; letter-spacing: .12em; text-transform: uppercase; color: var(--c); }
  .cat { font-size: 8.5px; color: var(--dim); }
  .conf { margin-left: auto; font-size: 9px; color: var(--faint); }
  .mbody {
    font-size: 11px; line-height: 1.45; color: var(--muted);
    display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden;
  }
  .mdate { font-size: 8.5px; color: var(--faint); margin-top: 6px; letter-spacing: .04em; }
</style>
