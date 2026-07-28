<script>
  import { Handle, Position } from '@xyflow/svelte';
  let { data } = $props();
  const color = {
    accent: 'var(--accent)', ok: 'var(--ok)', err: 'var(--err)', warn: 'var(--warn)',
    info: 'var(--info)', semantic: 'var(--semantic)', episodic: 'var(--episodic)', off: 'var(--faint)'
  };
</script>

<div class="node" style="--c:{color[data.tone] ?? 'var(--faint)'}">
  <Handle type="target" position={Position.Top} style="opacity:0" />
  <div class="nhead">
    <span class="ntype mono">{data.type.replace('msg.', '').replace('tool.', '')}</span>
    <span class="nnum mono">{data.num}</span>
  </div>
  {#if data.text}<div class="ntext">{data.text}</div>{/if}
  <Handle type="source" position={Position.Bottom} style="opacity:0" />
</div>

<style>
  .node {
    width: 220px;
    background: var(--s2);
    border: 1px solid var(--line2);
    border-left: 2px solid var(--c);
    border-radius: var(--r);
    padding: 7px 10px;
    font-family: var(--font-sans);
  }
  .nhead { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; }
  .ntype { font-size: 9.5px; letter-spacing: .1em; text-transform: uppercase; color: var(--c); }
  .nnum { font-size: 9px; color: var(--faint); }
  .ntext { font-size: 11px; color: var(--muted); margin-top: 3px; line-height: 1.4; overflow: hidden; }
</style>
