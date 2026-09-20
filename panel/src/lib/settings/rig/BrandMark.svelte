<script lang="ts">
  // A module's mark: its brand's icon when the module's own strings name one,
  // a neutral box when they do not. A vector mark is inlined and tinted by the
  // card; a brand's own artwork (or a raster icon) is shown as it is.
  import { Box } from 'lucide-svelte';
  import type { Brand } from '../../brands';

  let { brand, size = 20 }: { brand: Brand | null; size?: number } = $props();
</script>

<span class="bm" class:own={brand?.own} style="--s:{size}px" aria-hidden="true">
  {#if brand?.svg}{@html brand.svg}
  {:else if brand?.url}<img src={brand.url} alt="" width={size} height={size} />
  {:else}<Box size={size - 3} strokeWidth={1.6} />{/if}
</span>

<style>
  .bm {
    width: var(--s); height: var(--s); flex-shrink: 0;
    display: inline-flex; align-items: center; justify-content: center;
    color: var(--c, var(--dim));
  }
  .bm :global(svg) { width: 100%; height: 100%; display: block; }
  .bm img { width: 100%; height: 100%; display: block; border-radius: 22%; }
</style>
