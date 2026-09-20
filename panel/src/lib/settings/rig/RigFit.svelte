<script lang="ts">
  // Lives inside <SvelteFlow> to reach its viewport. SvelteFlow frames the
  // graph once, on mount; this frames it again when the graph changes shape —
  // a stranger appears on a card, the embedder moves to the CPU — or when the
  // canvas is resized. Live meter updates do not change `shape`, so the view
  // never jumps while the user is reading it.
  import { useSvelteFlow } from '@xyflow/svelte';

  let { shape, options }: { shape: string; options: { padding: number; maxZoom: number } } = $props();
  const { fitView } = useSvelteFlow();

  $effect(() => {
    void shape;
    // after the new cards have been measured
    const t = setTimeout(() => void fitView({ ...options, duration: 180 }), 60);
    return () => clearTimeout(t);
  });
</script>
