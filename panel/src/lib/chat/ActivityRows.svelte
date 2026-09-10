<script lang="ts">
  import { Terminal, FileText, Search, Globe, Brain, Monitor, Server, ChevronRight, Check, X, Loader, CircleHelp } from 'lucide-svelte';
  import { groupActivity, activityNotePreview, type ActivityItem } from './activity';

  let { items }: { items: ActivityItem[] } = $props();
  const rows = $derived(groupActivity(items));
  let openStep = $state<string | null>(null);
  let fullOutput = $state<string[]>([]);
  const OUTPUT_CAP = 4000;
  const icons: Record<string, typeof Terminal> = {
    bash: Terminal, read: FileText, write: FileText, edit: FileText, outline_file: FileText,
    grep: Search, find_symbol: Search, find_references: Search, file_map: Search,
    web_fetch: Globe, remember: Brain, check_page: Monitor, serve: Server,
  };
  const status = (item: ActivityItem) => item.status === 'run' ? 'running' : item.status === 'fail' ? 'failed' : item.status === 'ok' ? 'done' : 'status unavailable';
  const argsText = (item: ActivityItem) => item.args ? JSON.stringify(item.args, null, 2) :
    typeof item.event.payload?.raw_args === 'string' ? item.event.payload.raw_args : '';
</script>

{#snippet tool(item: ActivityItem)}
  {@const Icon = icons[item.name ?? ''] ?? Terminal}
  <div class="tool" class:done={item.status === 'ok'} class:fail={item.status === 'fail'} data-event-id={item.id}>
    <button class="toolhead" onclick={() => openStep = openStep === item.id ? null : item.id}
      aria-expanded={openStep === item.id} aria-label={`${item.name} ${item.arg ?? ''} ${status(item)}, toggle details`}>
      <span class="tstate">
        {#if item.status === 'run'}<Loader size={13} />
        {:else if item.status === 'fail'}<X size={13} />
        {:else if item.status === 'ok'}<Check size={13} />
        {:else}<CircleHelp size={13} />{/if}
      </span>
      <Icon size={13} class="ticon" />
      <span class="tname">{item.name}</span>
      <span class="target"><code>{item.arg}</code>{#if item.scope}<small>{item.scope}</small>{/if}</span>
      {#if item.status === 'run'}<small class="running">Running</small>{/if}
      <ChevronRight class={openStep === item.id ? 'tchev open' : 'tchev'} size={13} />
    </button>
    {#if openStep === item.id}
      <div class="toolinfo">
        <div class="receipt"><span>{item.kind === 'result' ? 'Result' : 'Call'} {item.event.id}</span><time datetime={item.event.ts}>{item.event.ts}</time></div>
        {#if item.event.payload?.id || item.event.payload?.run_id}
          <div class="receipt"><span>Tool call {String(item.event.payload?.id ?? 'not recorded')}</span><span>Run {String(item.event.payload?.run_id ?? 'not recorded')}</span></div>
        {/if}
        {#if argsText(item)}<pre aria-label="Tool arguments">{argsText(item)}</pre>{/if}
        {#if item.result}
          <div class="receipt"><span>Result {item.result.id} · {status(item)}</span><time datetime={item.result.ts}>{item.result.ts}</time></div>
        {:else if item.kind === 'result'}
          <p>Result without a matching call in this view.</p>
        {:else}<p>Waiting for the recorded result.</p>{/if}
        {#if item.output}
          <pre aria-label="Tool output">{item.output.length > OUTPUT_CAP && !fullOutput.includes(item.id) ? item.output.slice(0, OUTPUT_CAP) : item.output}</pre>
          {#if item.output.length > OUTPUT_CAP && !fullOutput.includes(item.id)}
            <button class="showall" onclick={() => fullOutput = [...fullOutput, item.id]}>Show full output ({item.output.length.toLocaleString()} characters)</button>
          {/if}
        {/if}
      </div>
    {/if}
  </div>
{/snippet}

<div class="steps">
  {#each rows as row (row.id)}
    {#if row.kind === 'reads'}
      <details class="readgroup" data-read-group={row.path}>
        <summary>
          <Check size={13} class="group-ok" />
          <FileText size={13} />
          <span class="grouptext"><span>Read <code>{row.path}</code> · {row.items.length} calls</span>{#if row.scope}<small>{row.scope}</small>{/if}</span>
          <ChevronRight size={13} class="group-chevron" />
        </summary>
        <div class="groupitems">
          {#each row.items as item (item.id)}{@render tool(item)}{/each}
        </div>
      </details>
    {:else if row.kind === 'tool' || row.kind === 'result'}
      {@render tool(row)}
    {:else}
      {@const preview = activityNotePreview(row)}
      {#if preview}
        <details class="technical-note" data-event-id={row.id}>
          <summary>{preview}</summary>
          <p>{row.text}</p>
        </details>
      {:else}
      <div class="step" class:stalled={row.noteKind === 'no_progress' || row.noteKind === 'breaker_tripped'}
        class:compaction={row.noteKind === 'context_compacted'} class:failure={row.kind === 'error' || row.kind === 'abort'}
        data-event-id={row.id}>
        <span class="sd"></span><span class="stext" title={row.text}>{row.text}</span>
      </div>
      {/if}
    {/if}
  {/each}
</div>

<style>
  .steps { display: flex; flex-direction: column; gap: 2px; margin: 8px 0 0; min-width: 0; }
  .technical-note { margin: var(--sp-3) 0; color: var(--muted); font-size: var(--fs-small); }
  .technical-note summary { display: list-item; list-style: disclosure-closed; padding: var(--sp-3) var(--sp-4); margin-left: var(--sp-6); width: auto; }
  .technical-note[open] summary { list-style: disclosure-open; }
  .technical-note p { margin: var(--sp-4) var(--sp-6); padding-left: var(--sp-5); border-left: 1px solid var(--line2); line-height: 1.65; white-space: pre-wrap; overflow-wrap: anywhere; }
  .step { display: flex; align-items: baseline; gap: 8px; font-size: 11.5px; color: var(--muted); padding: 3px; }
  .sd { width: 4px; height: 4px; border-radius: 50%; background: currentColor; flex-shrink: 0; }
  .stext { min-width: 0; overflow-wrap: anywhere; }
  .step.stalled, .step.compaction { color: var(--warn); }
  .step.failure { color: var(--err); }
  .tool { min-width: 0; }
  .toolhead, summary { display: flex; align-items: center; gap: 8px; width: 100%; padding: 7px 10px; text-align: left; background: transparent; border: 0; color: var(--muted); font: inherit; font-size: 11.5px; cursor: pointer; border-radius: var(--r-lg); }
  .toolhead:hover, summary:hover { background: var(--s2); color: var(--text); }
  .toolhead:focus-visible, summary:focus-visible, .showall:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .tstate { display: inline-flex; flex-shrink: 0; }
  .tool.done .tstate, summary :global(.group-ok) { color: var(--muted); }
  .tool.fail .tstate { color: var(--err); }
  .toolhead :global(svg), summary :global(svg) { flex-shrink: 0; }
  .tname, code { font-family: var(--font-mono); }
  .tname { flex-shrink: 0; }
  .target, .grouptext { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .target code, .grouptext > span { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--muted); }
  .tool.fail .target code, .toolhead:hover .target code { color: var(--text); }
  small { font-size: 11px; color: var(--muted); overflow-wrap: anywhere; }
  .running { flex-shrink: 0; }
  .toolhead :global(.tchev.open), details[open] > summary :global(.group-chevron) { transform: rotate(90deg); }
  summary { list-style: none; }
  summary::-webkit-details-marker { display: none; }
  .groupitems { padding-left: 12px; }
  .toolinfo { margin: 0 10px 6px; padding: 9px 12px; background: var(--s1); font-size: 11px; color: var(--muted); overflow-wrap: anywhere; }
  .receipt { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px 12px; margin-bottom: 6px; }
  .receipt time { font-variant-numeric: tabular-nums; }
  pre { margin: 0 0 9px; font: 11px/1.5 var(--font-mono); white-space: pre-wrap; overflow-wrap: anywhere; max-height: 260px; overflow: auto; }
  .showall { padding: 4px 0; border: 0; background: transparent; color: var(--text); cursor: pointer; text-decoration: underline; text-underline-offset: 3px; }
</style>
