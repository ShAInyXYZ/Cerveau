<script>
  import { SvelteFlow, Background } from '@xyflow/svelte';
  import '@xyflow/svelte/dist/style.css';
  import MemNode from './MemNode.svelte';
  import MemEdge from './MemEdge.svelte';

  let { docs = [], matchIds = new Set(), focusId = null, onFocus } = $props();
  const nodeTypes = { mem: MemNode };
  const edgeTypes = { floating: MemEdge };

  // ---- clustered layout: memories from the SAME session are related, so they
  // form a connected cluster. (The curator rarely writes related_to links, and
  // `sources` point to episodic docs that aren't in the graph — so session is the
  // real, always-present relationship to draw.) Each session = a ring of nodes
  // linked in a loop; explicit related_to / superseded_by edges kept when present.
  const layout = $derived.by(() => {
    const byId = new Map(docs.map((d) => [d.id, d]));
    const placed = new Map();
    const edges = [];

    // group by session
    const groups = new Map();   // session_id -> docs[]
    for (const d of docs) {
      const k = d.session_id || '(none)';
      if (!groups.has(k)) groups.set(k, []);
      groups.get(k).push(d);
    }
    const sessions = [...groups.keys()];

    // Cards are ~200×130px, so nodes need real spacing or they overlap. Radius of
    // a cluster is sized so members sit ~CARD apart on their local ring; clusters
    // are spread far enough on the big ring that they never collide.
    const CARD = 240;                                  // min center-to-center gap
    const maxMembers = Math.max(...[...groups.values()].map((m) => m.length), 1);
    const maxLocalR = maxMembers > 1 ? (CARD * maxMembers) / (2 * Math.PI) : 0;
    const bigR = Math.max(360, (sessions.length * (2 * maxLocalR + CARD)) / (2 * Math.PI));
    sessions.forEach((sid, gi) => {
      const ga = (gi / Math.max(1, sessions.length)) * Math.PI * 2;
      const gx = bigR * Math.cos(ga), gy = bigR * Math.sin(ga);
      const members = groups.get(sid);
      // local ring radius so members are ~CARD apart around the circle
      const localR = members.length > 1 ? (CARD * members.length) / (2 * Math.PI) : 0;

      members.forEach((d, j) => {
        const la = (j / Math.max(1, members.length)) * Math.PI * 2;
        placed.set(d.id, {
          x: gx + localR * Math.cos(la),
          y: gy + localR * Math.sin(la)
        });
      });
      // connect the cluster: link each member to the next (a loop) so the session
      // reads as one connected group
      if (members.length > 1) {
        for (let j = 0; j < members.length; j++) {
          const a = members[j], b = members[(j + 1) % members.length];
          edges.push({
            id: `s-${a.id}-${b.id}`, source: a.id, target: b.id, type: 'floating',
            style: 'stroke: var(--line2); stroke-width: 1; opacity: .6;'
          });
        }
      }
    });

    // stronger explicit links (related_to / supersede) drawn on top, in accent
    for (const d of docs) {
      const strong = [...(d.related_to ?? []), ...(d.superseded_by ? [d.superseded_by] : [])]
        .filter((id) => byId.has(id));
      for (const lid of strong) {
        edges.push({
          id: `r-${d.id}-${lid}`, source: d.id, target: lid, type: 'floating',
          style: 'stroke: var(--accent-line); stroke-width: 1.5;'
        });
      }
    }

    const nodes = docs.map((d) => {
      const state = focusId === d.id ? 'focus'
        : matchIds.size ? (matchIds.has(d.id) ? 'match' : 'dim')
        : 'normal';
      return {
        id: d.id, type: 'mem',
        position: placed.get(d.id) ?? { x: 0, y: 0 },
        data: { type: d.memory_type, content: d.content, category: d.category, confidence: d.confidence, ts: d.ts, session_id: d.session_id, state }
      };
    });
    return { nodes, edges };
  });

  let nodes = $state([]);
  let edges = $state([]);
  $effect(() => { nodes = layout.nodes; edges = layout.edges; });

  function onNodeClick({ node }) { onFocus?.(node.id); }
</script>

<div class="wrap">
  <SvelteFlow bind:nodes bind:edges {nodeTypes} {edgeTypes} fitView colorMode="dark"
    nodesDraggable={true} nodesConnectable={false} elementsSelectable={true}
    onnodeclick={onNodeClick} proOptions={{ hideAttribution: true }}
    minZoom={0.2} maxZoom={1.6}>
    <Background bgColor="var(--bg)" patternColor="var(--line)" gap={26} />
  </SvelteFlow>
</div>

<style>
  .wrap { width: 100%; height: 100%; }
  .wrap :global(.svelte-flow) { background: var(--bg); }
</style>
