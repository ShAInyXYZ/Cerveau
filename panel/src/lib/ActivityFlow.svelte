<script>
  import { SvelteFlow, Background } from '@xyflow/svelte';
  import '@xyflow/svelte/dist/style.css';
  import ActivityNode from './ActivityNode.svelte';

  let { ticks = [] } = $props();

  const nodeTypes = { ev: ActivityNode };

  const TONE = {
    'msg.user': 'accent', 'msg.assistant': 'ok',
    'tool.call': 'info', 'tool.result': 'info',
    'error': 'err', 'checkpoint': 'episodic', 'turn.close': 'off',
    'note': 'off', 'plan': 'semantic', 'aborted': 'warn', 'interrupt': 'warn'
  };

  function textOf(ev) {
    const p = ev.payload ?? {};
    return String(p.name ?? p.text ?? p.output ?? p.kind ?? p.detail ?? p.stop ?? '').slice(0, 60);
  }

  let flow = $derived.by(() => {
    const evts = ticks.slice(-30);
    const nodes = evts.map((ev, i) => ({
      id: ev.id ?? `n${i}`,
      type: 'ev',
      position: { x: 0, y: i * 74 },
      data: {
        type: ev.type,
        num: (ev.id ?? '').replace('evt_', ''),
        text: textOf(ev),
        tone: TONE[ev.type] ?? 'off'
      }
    }));
    const edges = evts.slice(1).map((ev, i) => ({
      id: `e${i}`, source: evts[i].id ?? `n${i}`, target: ev.id ?? `n${i + 1}`,
      type: 'smoothstep', style: 'stroke: var(--line2); stroke-width: 1;'
    }));
    return { nodes, edges };
  });

  let nodes = $state([]);
  let edges = $state([]);
  $effect(() => { nodes = flow.nodes; edges = flow.edges; });
</script>

<div class="wrap">
  <SvelteFlow bind:nodes bind:edges {nodeTypes} fitView colorMode="dark"
    nodesDraggable={false} nodesConnectable={false} elementsSelectable={false}
    proOptions={{ hideAttribution: true }}>
    <Background bgColor="var(--s1)" patternColor="var(--line)" gap={20} />
  </SvelteFlow>
</div>

<style>
  .wrap { width: 100%; height: 100%; }
  .wrap :global(.svelte-flow) { background: var(--s1); }
  .wrap :global(.svelte-flow__edge-path) { stroke: var(--line2); }
</style>
