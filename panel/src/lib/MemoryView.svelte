<script>
  import { j, jpost } from './api.js';
  import { Segmented, Button, Dot, Select } from '../kit/index.js';
  import MemGraph from './MemGraph.svelte';
  import { Search, GitBranch } from 'lucide-svelte';

  let view = $state('table');          // table | graph
  let query = $state('');
  let results = $state([]);
  let searched = $state(false);
  let review = $state([]);
  let prov = $state(null);

  let graphDocs = $state([]);
  let matchIds = $state(new Set());
  let focusId = $state(null);

  // filters — apply to BOTH table and graph. Default to SEMANTIC: episodic is raw
  // tool-output noise (the source material); semantic is the distilled summary,
  // which is what the browser should foreground.
  let typeFilter = $state('semantic'); // all | semantic | episodic
  let sessionFilter = $state('all');   // all | <session_id>
  let allDocs = $state([]);            // full unfiltered list from /memory/list

  // session options derived from whatever's actually in memory
  const sessionOptions = $derived.by(() => {
    const ids = [...new Set(allDocs.map((d) => d.session_id).filter(Boolean))];
    return ids.sort().reverse(); // newest-ish first (ids are timestamped)
  });

  // the current filter applied to any doc set (shared by table + graph)
  function passesFilter(d) {
    return (typeFilter === 'all' || d.memory_type === typeFilter) &&
           (sessionFilter === 'all' || d.session_id === sessionFilter);
  }
  function shortSession(id) { return id ? id.replace(/^\d{8}-\d{6}-/, '') || id.slice(0, 14) : id; }
  function fmtDate(ts) {
    if (!ts) return '';
    const d = new Date(ts * 1000); // stored as unix seconds
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) +
      ' · ' + d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  }

  // the table shows the FULL memory list (all types) by default, narrowed by the
  // type/session filters — or the search results when a query is active.
  const tableRows = $derived.by(() => {
    if (searched) return results;
    return allDocs.filter(passesFilter);
  });

  // graph uses the SAME filtered docs (was always showing everything). Falls back
  // to the full loaded set so it isn't empty before the first list load.
  const graphNodes = $derived((allDocs.length ? allDocs : graphDocs).filter(passesFilter));

  const typeCounts = $derived.by(() => {
    const c = { semantic: 0, episodic: 0 };
    for (const d of allDocs) if (d.memory_type in c) c[d.memory_type]++;
    return c;
  });

  async function loadList() {
    const d = await j('/api/memory/list');
    allDocs = d?.results ?? [];
  }

  async function search() {
    const q = query.trim();
    if (!q) { results = []; searched = false; matchIds = new Set(); return; }
    searched = true;
    const d = await j(`/api/memory/search?q=${encodeURIComponent(q)}`);
    results = d?.results ?? [];
    const ids = new Set(results.map((r) => r.id));
    matchIds = ids;
    focusId = ids.size === 1 ? [...ids][0] : null;
    const known = new Set(graphDocs.map((g) => g.id));
    const extra = results.filter((r) => !known.has(r.id));
    if (extra.length) graphDocs = [...graphDocs, ...extra];
  }
  function clearSearch() { query = ''; results = []; searched = false; matchIds = new Set(); focusId = null; }

  async function loadGraph() {
    const d = await j('/api/memory/graph');
    graphDocs = d?.nodes ?? [];
  }
  async function loadReview() { const d = await j('/api/memory/review'); review = d?.review ?? []; }
  async function resolve(id, action) { await jpost(`/api/memory/review/${encodeURIComponent(id)}`, { action }); loadReview(); }
  async function showProv(id) { prov = await j(`/api/memory/provenance/${encodeURIComponent(id)}`); }

  $effect(() => { loadReview(); loadGraph(); loadList(); });
</script>

{#snippet filterBar()}
  <div class="filterbar">
    <div class="fgroup">
      <span class="flabel">TYPE</span>
      <button class="chip" class:on={typeFilter === 'all'} onclick={() => (typeFilter = 'all')}>All <span class="cn">{allDocs.length}</span></button>
      <button class="chip sem" class:on={typeFilter === 'semantic'} onclick={() => (typeFilter = 'semantic')}>Semantic <span class="cn">{typeCounts.semantic}</span></button>
      <button class="chip epi" class:on={typeFilter === 'episodic'} onclick={() => (typeFilter = 'episodic')}>Episodic <span class="cn">{typeCounts.episodic}</span></button>
    </div>
    {#if sessionOptions.length}
      <div class="fgroup">
        <span class="flabel">SESSION</span>
        <Select bind:value={sessionFilter}
          options={[{ value: 'all', label: 'All sessions' }, ...sessionOptions.map((sid) => ({ value: sid, label: shortSession(sid) }))]} />
      </div>
    {/if}
  </div>
{/snippet}

<div class="mem">
  <div class="topbar">
    <div class="searchwrap">
      <span class="sicon"><Search size={14} /></span>
      <input bind:value={query} placeholder="search all memory…"
        onkeydown={(e) => { if (e.key === 'Enter') search(); if (e.key === 'Escape') clearSearch(); }} />
      {#if searched}<button class="clear" onclick={clearSearch}>clear</button>{/if}
    </div>
    <Segmented options={[{ value: 'table', label: 'Table' }, { value: 'graph', label: 'Graph' }]} bind:value={view} />
  </div>

  {#if view === 'graph'}
    <div class="graphhost">
      {#if !searched}<div class="gfilters">{@render filterBar()}</div>{/if}
      {#if graphNodes.length === 0}
        <div class="ghint label"><GitBranch size={14} /> NO MEMORY GRAPH YET</div>
      {:else}
        <MemGraph docs={graphNodes} {matchIds} {focusId} onFocus={(id) => (focusId = id)} />
      {/if}
      {#if searched}
        <div class="gsearchnote label">{matchIds.size} MATCH{matchIds.size === 1 ? '' : 'ES'} · CLICK A NODE TO FOCUS</div>
      {/if}
    </div>
  {:else}
    <div class="scroll">
      {#if !searched}<div class="tfilters">{@render filterBar()}</div>{/if}

      {#if review.length > 0}
        <div class="sect"><span class="label">REVIEW QUEUE</span><span class="tag">{review.length}</span></div>
        {#each review as d (d.id)}
          <div class="row review">
            <div class="rmeta">
              <span class="badge semantic">semantic</span>
              {#if d.category}<span class="mtag">{d.category}</span>{/if}
              <span class="mtag warn">~ similar exists</span>
            </div>
            <div class="rbody">{d.content}</div>
            <div class="racts">
              <Button size="sm" onclick={() => resolve(d.id, 'keep')}>keep both</Button>
              <Button size="sm" variant="ghost" onclick={() => resolve(d.id, 'supersede')}>supersede</Button>
            </div>
          </div>
        {/each}
      {/if}

      <div class="sect">
        <span class="label">{searched ? 'RESULTS' : 'ALL MEMORY'}</span>
        <span class="tag">{tableRows.length}</span>
      </div>
      {#if tableRows.length === 0}
        <div class="none label">{searched ? 'NO RESULTS' : 'NO MEMORIES YET'}</div>
      {/if}
      {#each tableRows as d (d.id)}
          <div class="row">
            <div class="rmeta">
              <span class="badge {d.memory_type}">{d.memory_type}</span>
              {#if d.category}<span class="mtag">{d.category}</span>{/if}
              {#if d.confidence}<span class="mtag">conf {Math.round(d.confidence * 100)}</span>{/if}
              {#if d.evt_type}<span class="mtag">{d.evt_type}</span>{/if}
              {#if d.ts}<span class="rdate">{fmtDate(d.ts)}</span>{/if}
            </div>
            <div class="rbody">{d.content}</div>
            {#if d.memory_type === 'semantic' && d.sources?.length}
              <button class="prov" onclick={() => showProv(d.id)}>view provenance</button>
            {/if}
          </div>
        {/each}
    </div>
  {/if}
</div>

{#if prov}
  <div class="provoverlay" onclick={() => (prov = null)} role="presentation">
    <div class="provcard" onclick={(e) => e.stopPropagation()} role="presentation">
      <div class="sect"><span class="label">PROVENANCE</span></div>
      <div class="provlead">{prov.doc.content}</div>
      {#if prov.events.length === 0}<div class="none label">NO SOURCE EVENTS ON DISK</div>{/if}
      {#each prov.events as pe}
        <div class="row">
          <div class="rmeta"><span class="badge episodic">{pe.event.type}</span><span class="mtag">{pe.session}</span><span class="mtag">{pe.event.id}</span></div>
          <div class="rbody">{pe.event.payload?.text ?? pe.event.payload?.output ?? ''}</div>
        </div>
      {/each}
      <Button size="sm" variant="ghost" onclick={() => (prov = null)}>close</Button>
    </div>
  </div>
{/if}

<style>
  .mem { flex: 1; min-width: 0; display: flex; flex-direction: column; min-height: 0; }

  .topbar {
    flex-shrink: 0; display: flex; align-items: center; gap: 12px;
    width: 100%; max-width: 780px; margin: 0 auto;
    padding: 18px 22px 12px;
  }
  .searchwrap {
    flex: 1; display: flex; align-items: center; gap: 8px;
    background: var(--surface-raised); border-radius: 999px;
    box-shadow: var(--elev-1); padding: 4px 6px 4px 14px;
  }
  .sicon { display: flex; color: var(--dim); flex-shrink: 0; }
  .searchwrap input {
    flex: 1; background: transparent; border: none; outline: none;
    color: var(--text); font-size: 13px; padding: 6px 0;
  }
  .searchwrap input::placeholder { color: var(--faint); }
  .clear {
    font-family: var(--font-mono); font-size: 9px; letter-spacing: .1em; text-transform: uppercase;
    color: var(--dim); background: color-mix(in srgb,#fff 4%,transparent);
    border: none; border-radius: 999px; padding: 5px 11px; cursor: pointer; flex-shrink: 0;
  }
  .clear:hover { color: var(--text); }

  /* table view */
  .scroll { flex: 1; overflow-y: auto; width: 100%; max-width: 780px; margin: 0 auto; padding: 0 22px 22px; display: flex; flex-direction: column; gap: 8px; }
  .sect { display: flex; align-items: center; gap: 8px; margin: 12px 0 2px; }
  .none { display: flex; align-items: center; gap: 8px; justify-content: center; padding: 30px; color: var(--faint); }

  /* filter bar */
  /* one filter bar — a centered floating card, same design in table + graph */
  .filterbar {
    display: inline-flex; flex-wrap: wrap; align-items: center; gap: 10px 18px;
    padding: 8px 14px; border-radius: 12px;
    background: var(--surface); box-shadow: 0 0 0 1px var(--line2), var(--elev-1);
  }
  .tfilters { display: flex; justify-content: center; padding: 4px 0 14px; }
  .fgroup { display: flex; align-items: center; gap: 7px; }
  .flabel {
    font-family: var(--font-mono); font-size: 9px; letter-spacing: .14em;
    color: var(--faint); margin-right: 1px;
  }
  .chip {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 5px 10px; border-radius: 999px; cursor: pointer;
    font-size: 12px; color: var(--muted);
    background: var(--s2); border: 1px solid var(--line2);
    transition: color .12s, border-color .12s, background .12s;
  }
  .chip:hover { color: var(--text); }
  .chip .cn {
    font-family: var(--font-mono); font-size: 10px; color: var(--faint);
    font-variant-numeric: tabular-nums;
  }
  /* active state per type, color-coded */
  .chip.on { color: var(--text); background: var(--s3); border-color: var(--faint); }
  .chip.sem.on { border-color: color-mix(in srgb, var(--semantic) 55%, transparent); background: color-mix(in srgb, var(--semantic) 12%, transparent); }
  .chip.epi.on { border-color: color-mix(in srgb, var(--episodic) 55%, transparent); background: color-mix(in srgb, var(--episodic) 12%, transparent); }
  .chip.sem.on .cn { color: var(--semantic); }
  .chip.epi.on .cn { color: var(--episodic); }


  .row {
    --c: var(--faint);
    background: linear-gradient(180deg, color-mix(in srgb, var(--c) 3%, var(--s1)) 0%, var(--s1) 46%);
    border-radius: 10px;
    box-shadow: 0 0 0 1px color-mix(in srgb, var(--c) 11%, transparent), 0 4px 12px -8px rgba(0,0,0,.5);
    padding: 12px 15px;
  }
  .row:has(.badge.semantic) { --c: var(--semantic); }
  .row:has(.badge.episodic) { --c: var(--episodic); }
  .row.review { --c: var(--warn); }
  .rmeta { display: flex; flex-wrap: wrap; align-items: center; gap: 7px; margin-bottom: 7px; }
  .rdate { margin-left: auto; font-family: var(--font-mono); font-size: 10px; color: var(--faint); font-variant-numeric: tabular-nums; }
  .badge {
    font-family: var(--font-mono); font-size: 8.5px; letter-spacing: .14em; text-transform: uppercase;
    padding: 2px 8px; border-radius: 999px;
    color: var(--c); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--c) 35%, transparent);
  }
  .badge.semantic { --c: var(--semantic); }
  .badge.episodic { --c: var(--episodic); }
  .mtag { font-family: var(--font-mono); font-size: 9px; color: var(--dim); }
  .mtag.warn { color: var(--warn); }
  .rbody { font-size: 13px; line-height: 1.55; color: var(--text); }
  .racts { display: flex; gap: 6px; margin-top: 10px; }
  .prov {
    align-self: flex-start; margin-top: 9px;
    font-family: var(--font-mono); font-size: 9.5px; letter-spacing: .08em; text-transform: uppercase;
    color: var(--semantic); background: transparent; border: none; cursor: pointer; padding: 0;
  }
  .prov:hover { text-decoration: underline; }

  /* graph view */
  .graphhost { flex: 1; position: relative; min-height: 0; }
  /* filter bar floats over the graph canvas, top-left */
  /* graph: same bar, floated top-center over the canvas */
  .gfilters {
    position: absolute; top: 12px; left: 50%; transform: translateX(-50%); z-index: 10;
  }
  .ghint, .gsearchnote {
    position: absolute; z-index: 4; display: flex; align-items: center; gap: 8px;
  }
  .ghint { inset: 0; justify-content: center; }
  .gsearchnote {
    left: 50%; bottom: 16px; transform: translateX(-50%);
    background: var(--surface-raised); box-shadow: var(--elev-1);
    padding: 7px 14px; border-radius: 999px; color: var(--dim);
  }

  /* provenance overlay */
  .provoverlay {
    position: fixed; inset: 0; z-index: 80;
    background: rgba(0,0,0,.5); backdrop-filter: blur(2px);
    display: flex; align-items: center; justify-content: center; padding: 40px;
  }
  .provcard {
    width: 100%; max-width: 560px; max-height: 80vh; overflow-y: auto;
    background: var(--surface); border-radius: 12px; box-shadow: var(--elev-2);
    padding: 18px; display: flex; flex-direction: column; gap: 8px;
  }
  .provlead { font-size: 13px; color: var(--muted); line-height: 1.5; margin-bottom: 4px; }
</style>
