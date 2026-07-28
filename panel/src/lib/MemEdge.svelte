<script module>
  import { Position } from '@xyflow/svelte';

  function center(node) {
    const w = node.measured?.width ?? node.width ?? 200;
    const h = node.measured?.height ?? node.height ?? 120;
    return { x: node.internals.positionAbsolute.x + w / 2, y: node.internals.positionAbsolute.y + h / 2, w, h };
  }
  // the point on a node's border facing the other node, and which side it is
  function borderPoint(node, tx, ty) {
    const { x: cx, y: cy, w, h } = center(node);
    const dx = tx - cx, dy = ty - cy;
    if (dx === 0 && dy === 0) return { x: cx, y: cy, pos: Position.Top };
    const sx = w / 2, sy = h / 2;
    const scale = 1 / Math.max(Math.abs(dx) / sx, Math.abs(dy) / sy);
    let pos;
    if (Math.abs(dx) / sx > Math.abs(dy) / sy) pos = dx > 0 ? Position.Right : Position.Left;
    else pos = dy > 0 ? Position.Bottom : Position.Top;
    return { x: cx + dx * scale, y: cy + dy * scale, pos };
  }
  export function edgeParams(source, target) {
    const s = borderPoint(source, center(target).x, center(target).y);
    const t = borderPoint(target, center(source).x, center(source).y);
    return { sx: s.x, sy: s.y, sourcePos: s.pos, tx: t.x, ty: t.y, targetPos: t.pos };
  }
</script>

<script>
  import { getBezierPath, useInternalNode, BaseEdge } from '@xyflow/svelte';
  let { id, source, target, style } = $props();

  const sourceNode = useInternalNode(source);
  const targetNode = useInternalNode(target);

  const path = $derived.by(() => {
    const s = sourceNode.current, t = targetNode.current;
    if (!s || !t) return null;
    const { sx, sy, sourcePos, tx, ty, targetPos } = edgeParams(s, t);
    return getBezierPath({ sourceX: sx, sourceY: sy, sourcePosition: sourcePos, targetX: tx, targetY: ty, targetPosition: targetPos })[0];
  });
</script>

{#if path}<BaseEdge {id} {path} {style} />{/if}
