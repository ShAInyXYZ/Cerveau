<script lang="ts">
  let { pack }: { pack: { origin?: string; ignored_installed?: { path: string; version?: string }[] } } = $props();
  const ignored = $derived(pack.ignored_installed ?? []);
</script>

{#if pack.origin === 'builtin'}
  <div class="source">
    <p>Built into Cerveau. Takes precedence over installed copies.</p>
    {#if ignored.length}
      <details>
        <summary>{ignored.length} installed {ignored.length === 1 ? 'copy' : 'copies'} ignored; files unchanged</summary>
        <ul>
          {#each ignored as copy}
            <li>{copy.path}{copy.version ? ` · v${copy.version}` : ''}</li>
          {/each}
        </ul>
      </details>
    {/if}
  </div>
{/if}

<style>
  .source { padding: 0 12px 10px; color: var(--text); font-size: 14px; line-height: 1.45; }
  p { margin: 0; }
  details { margin-top: 6px; }
  summary { cursor: pointer; }
  summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
  ul { margin: 6px 0 0; padding-inline-start: 18px; }
  li { overflow-wrap: anywhere; }
</style>
