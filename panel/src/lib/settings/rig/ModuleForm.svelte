<script lang="ts">
  // The form behind a module: where it is connected, and the controls to give
  // it more, less or none — with the diagram redrawing as each one changes and
  // the core's verdict on whether the result would run on this machine.
  //
  // It edits nothing itself. Every change goes to the parent as a whole new
  // placement; the parent asks the core what that placement means.
  import { X, Minus, Plus, Check } from 'lucide-svelte';
  import BrandMark from './BrandMark.svelte';
  import type { RigCardUse, RigGPU, RigPlan, RigSavedLayout } from '../../types';
  import { gb, shortGPU as short, describePlacement, type Placement, type RigEdge, type WorkloadData } from './rigGraph';

  let {
    id, module, placement, gpus, observed, links, plan, checking, dirty, editable, hasCPU,
    layouts, saving,
    onedit, onevaluate, onsave, onsaveas, oncancel, onclose, onload, onforget,
  }: {
    id: string; module: WorkloadData; placement: Placement;
    gpus: RigGPU[]; observed: RigCardUse[]; links: RigEdge[];
    plan: RigPlan | null; checking: boolean; dirty: boolean; editable: boolean; hasCPU: boolean;
    layouts: RigSavedLayout[]; saving: boolean;
    onedit: (p: Placement) => void; onevaluate: () => void;
    onsave: () => void; onsaveas: (name: string) => void; oncancel: () => void; onclose: () => void;
    onload: (l: RigSavedLayout) => void; onforget: (name: string) => void;
  } = $props();

  const movable = $derived(editable && (id === 'core' || id === 'embedder'));

  // what each card holds that is not this module — what it would have to share with
  function others(gpu: number): number {
    const u = observed.find((o) => o.gpu === gpu);
    if (!u) return 0;
    const mine = u.consumers.filter((c) => (id === 'core' ? c.role === 'core' : id === 'embedder' ? c.role === 'embedder' : false))
      .reduce((a, c) => a + c.mem, 0);
    return Math.max(0, u.used - mine);
  }

  // ── Core ──
  function toggleCard(gpu: number) {
    const on = placement.gpus.includes(gpu);
    onedit({ ...placement, gpus: on ? placement.gpus.filter((g) => g !== gpu) : [...placement.gpus, gpu].sort((a, b) => a - b) });
  }
  const share = $derived(Math.round((placement.share || plan?.gpu_util || 0) * 100));
  function setShare(pct: number) {
    const v = Math.min(98, Math.max(20, Math.round(pct)));
    if (v !== share) onedit({ ...placement, share: v / 100 });
  }
  const sizes = $derived(plan?.fit?.group_sizes ?? []);

  // ── Embedder ──
  function placeEmbedder(gpu: number | null) {
    onedit({ ...placement, embed: gpu === null ? { device: 'cpu', gpus: [] } : { device: 'cuda', gpus: [gpu] } });
  }

  // ── Save as ──
  let naming = $state(false);
  let name = $state('');
  function commitName() {
    if (name.trim()) onsaveas(name.trim());
    naming = false; name = '';
  }
  const VERDICT = { fits: 'Fits', tight: 'Fits, barely', no: 'Does not fit', unknown: 'Cannot tell' } as const;
</script>

<aside class="form {module.role}" style="--c: {module.color}" aria-label="{module.title} — where it runs">
  <header>
    <BrandMark brand={module.brand} size={26} />
    <div class="who">
      <span class="role mono">{module.role === 'crv' ? 'cerveau' : module.role}</span>
      <h3>{module.title}</h3>
      {#if module.sub}<p>{module.sub}</p>{/if}
    </div>
    <button class="x" onclick={onclose} aria-label="close"><X size={14} /></button>
  </header>

  {#if id === 'core' && movable}
    <section>
      <div class="lab"><span>Runs on</span><em>{placement.gpus.length ? `${placement.gpus.length} card${placement.gpus.length === 1 ? '' : 's'}` : 'no card'}</em></div>
      <div class="cards">
        {#each gpus as g (g.index)}
          {@const on = placement.gpus.includes(g.index)}
          <button class="card" class:on role="switch" aria-checked={on} onclick={() => toggleCard(g.index)}>
            <span class="tick">{#if on}<Check size={11} strokeWidth={3} />{/if}</span>
            <span class="cname"><b>GPU {g.index}</b> {short(g.name)}</span>
            <span class="cmem mono">{others(g.index) > 64 ? `${gb(others(g.index))} GB in use` : `${gb(g.mem_total)} GB`}</span>
          </button>
        {/each}
      </div>
      {#if sizes.length}<p class="hint">This model splits across {sizes.join(', ').replace(/, ([^,]*)$/, ' or $1')} card{sizes.length === 1 && sizes[0] === 1 ? '' : 's'}.</p>{/if}
    </section>

    <section>
      <div class="lab"><span>Share of each card</span><em>{share}%</em></div>
      <div class="share">
        <button onclick={() => setShare(share - 2)} aria-label="allocate less"><Minus size={13} /></button>
        <input type="range" min="20" max="98" step="1" value={share} aria-label="share of each card"
          onchange={(e) => setShare(+e.currentTarget.value)} />
        <button onclick={() => setShare(share + 2)} aria-label="allocate more"><Plus size={13} /></button>
      </div>
      <p class="hint">How much of every card's memory the Core claims. What is left over is for everything else on it.</p>
    </section>
  {:else if id === 'embedder' && movable}
    <section>
      <div class="lab"><span>Runs on</span><em>{placement.embed.device === 'cuda' ? `GPU ${placement.embed.gpus.join(', ')}` : 'the CPU'}</em></div>
      <div class="cards" role="radiogroup" aria-label="where the embedder runs">
        {#if hasCPU}
          <button class="card" class:on={placement.embed.device === 'cpu'} role="radio" aria-checked={placement.embed.device === 'cpu'} onclick={() => placeEmbedder(null)}>
            <span class="tick round">{#if placement.embed.device === 'cpu'}<i></i>{/if}</span>
            <span class="cname"><b>CPU</b> no card needed</span>
          </button>
        {/if}
        {#each gpus as g (g.index)}
          {@const on = placement.embed.device === 'cuda' && placement.embed.gpus.includes(g.index)}
          <button class="card" class:on role="radio" aria-checked={on} onclick={() => placeEmbedder(g.index)}>
            <span class="tick round">{#if on}<i></i>{/if}</span>
            <span class="cname"><b>GPU {g.index}</b> {short(g.name)}</span>
            <span class="cmem mono">{placement.gpus.includes(g.index) ? "the Core's" : `${gb(g.mem_total)} GB`}</span>
          </button>
        {/each}
      </div>
    </section>
  {:else}
    <section>
      <div class="lab"><span>Runs on</span></div>
      {#each links as l (l.id)}
        <div class="ro"><span>{l.target === 'cpu' ? 'CPU' : l.target === 'ram' ? 'RAM' : `GPU ${l.target.slice(4)}`}</span><em class="mono">{l.label ?? ''}</em></div>
      {/each}
      <p class="hint">
        {#if id === 'core' || id === 'embedder'}This profile does not declare where it runs, so Cerveau cannot move it from here.
        {:else if module.role === 'other'}Found on the cards. It is not Cerveau's, so Cerveau does not place it — but the estimate counts the memory it holds.
        {:else}Part of Cerveau itself. It runs on the host and is not placed by hand.{/if}
      </p>
    </section>
  {/if}

  {#if movable}
    <section class="eval">
      <div class="lab">
        <span>Would it run?</span>
        <button class="link" disabled={checking} onclick={onevaluate}>{checking ? 'checking…' : 'Evaluate'}</button>
      </div>
      {#if plan}
        {#each plan.problems as p (p)}<p class="bad">{p}</p>{/each}
        {#each plan.warnings as w (w)}<p class="warn">{w}</p>{/each}
        {#if id === 'core' && plan.fit && !plan.problems.length}
          <p class="verdict {plan.fit.verdict}"><b>{VERDICT[plan.fit.verdict]}</b> <span>an estimate, from the checkpoint on disk</span></p>
          {#each plan.fit.reasons as r (r)}<p class="why">{r}</p>{/each}
          {#if plan.fit.cards.length && plan.fit.verdict !== 'unknown'}
            <table class="mono">
              <thead><tr><th>card</th><th>weights</th><th>KV room</th><th>window needs</th></tr></thead>
              <tbody>
                {#each plan.fit.cards as c (c.gpu)}
                  <tr><td>GPU {c.gpu}</td><td>{gb(c.weights)}</td><td>{gb(c.kv_room)}</td><td class:over={c.kv_needed > c.kv_room}>{gb(c.kv_needed)}</td></tr>
                {/each}
              </tbody>
            </table>
          {/if}
        {:else if !plan.problems.length && !plan.warnings.length}
          <p class="why">Nothing stands in the way of this placement.</p>
        {/if}
        {#if dirty && plan.changed.length && !plan.problems.length}
          <details><summary>What saving changes</summary>{#each plan.changed as c (c)}<p class="chg mono">{c}</p>{/each}</details>
        {/if}
      {:else if !checking}
        <p class="hint">Press Evaluate to check this placement against the machine.</p>
      {/if}
    </section>

    <section>
      <div class="lab"><span>Saved layouts</span></div>
      {#if layouts.length}
        {#each layouts as l (l.name)}
          <div class="layout">
            <button class="lname" onclick={() => onload(l)}><b>{l.name}</b><span>{describePlacement({ ...l, share: l.gpu_util })}</span></button>
            <button class="x small" onclick={() => onforget(l.name)} aria-label="forget the layout {l.name}"><X size={12} /></button>
          </div>
        {/each}
      {:else}
        <p class="hint">None yet. “Save as” keeps this placement under a name, to load again later without changing the profile.</p>
      {/if}
    </section>

    <footer>
      {#if naming}
        <!-- svelte-ignore a11y_autofocus -->
        <input class="nameit" bind:value={name} autofocus placeholder="name this layout…" aria-label="layout name"
          onkeydown={(e) => { if (e.key === 'Enter') commitName(); if (e.key === 'Escape') { naming = false; name = ''; } }} />
        <button class="ghost" onclick={() => { naming = false; name = ''; }}>Back</button>
        <button class="solid" disabled={!name.trim()} onclick={commitName}>Keep</button>
      {:else}
        <button class="ghost" disabled={!dirty} onclick={oncancel}>Cancel</button>
        <button class="ghost" disabled={saving || (plan?.problems.length ?? 0) > 0} onclick={() => (naming = true)}>Save as…</button>
        <button class="solid" disabled={!dirty || saving || checking || !plan || plan.problems.length > 0} onclick={onsave}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      {/if}
    </footer>
  {/if}
</aside>

<style>
  .form {
    --c: var(--dim);
    width: 336px; flex-shrink: 0; align-self: flex-start;
    max-height: calc(100vh - 210px); overflow-y: auto;
    background: var(--s1); border-radius: 10px; box-shadow: 0 0 0 1px var(--line2);
    border-top: 2px solid var(--c);
    display: flex; flex-direction: column;
  }

  header { display: flex; align-items: flex-start; gap: 11px; padding: 13px 12px 12px 16px; border-bottom: 1px solid var(--line); }
  .who { flex: 1; min-width: 0; }
  .role { font-size: var(--fs-micro); letter-spacing: .1em; text-transform: uppercase; color: var(--c); }
  h3 { margin: 2px 0 0; font-size: var(--fs-title); font-weight: 640; color: var(--text); }
  header p { margin: 1px 0 0; font-size: var(--fs-small); color: var(--muted); }
  .x {
    display: inline-flex; align-items: center; justify-content: center; width: 26px; height: 26px; flex-shrink: 0;
    border: none; border-radius: 6px; background: transparent; color: var(--dim); cursor: pointer;
  }
  .x:hover { color: var(--text); background: var(--s3); }
  .x.small { width: 22px; height: 22px; }

  section { padding: 13px 16px 14px; border-bottom: 1px solid var(--line); }
  .lab { display: flex; align-items: baseline; justify-content: space-between; gap: 10px; margin-bottom: 9px; }
  .lab span { font-size: var(--fs-body); font-weight: 620; color: var(--text); }
  .lab em { font-style: normal; font-size: var(--fs-small); color: var(--muted); font-variant-numeric: tabular-nums; }
  .hint { margin: 9px 0 0; font-size: var(--fs-small); line-height: 1.5; color: var(--dim); }

  /* a card the module can run on: one row, one click */
  .cards { display: flex; flex-direction: column; gap: 4px; }
  .card {
    display: flex; align-items: center; gap: 10px; width: 100%; min-height: 34px;
    padding: 0 10px; border: none; border-radius: 7px; cursor: pointer; text-align: left;
    background: transparent; box-shadow: inset 0 0 0 1px var(--line); color: var(--muted);
    font: inherit; font-size: var(--fs-small);
    transition: background var(--t-fast), box-shadow var(--t-fast), color var(--t-fast);
  }
  .card:hover { background: var(--s2); color: var(--text); }
  .card.on { background: var(--s2); box-shadow: inset 0 0 0 1px var(--c); color: var(--text); }
  .tick {
    width: 16px; height: 16px; flex-shrink: 0; border-radius: 4px;
    display: inline-flex; align-items: center; justify-content: center;
    box-shadow: inset 0 0 0 1px var(--line2); color: var(--accent-ink);
  }
  .tick.round { border-radius: 50%; }
  .tick.round i { width: 6px; height: 6px; border-radius: 50%; background: var(--accent-ink); }
  .card.on .tick { background: var(--c); box-shadow: none; }
  .cname { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .cname b { font-weight: 600; margin-right: 5px; }
  .cmem { flex-shrink: 0; font-size: var(--fs-micro); color: var(--dim); }

  .share { display: flex; align-items: center; gap: 8px; }
  .share button {
    display: inline-flex; align-items: center; justify-content: center; width: 28px; height: 28px; flex-shrink: 0;
    border: none; border-radius: 7px; background: var(--s2); box-shadow: inset 0 0 0 1px var(--line2); color: var(--muted); cursor: pointer;
  }
  .share button:hover { color: var(--text); background: var(--s3); }
  .share input {
    -webkit-appearance: none; appearance: none; flex: 1; min-width: 0; height: 4px; border-radius: 2px;
    background: var(--line2); outline: none; cursor: pointer;
  }
  .share input::-webkit-slider-thumb { -webkit-appearance: none; appearance: none; width: 14px; height: 14px; border-radius: 50%; background: var(--accent); border: none; }
  .share input::-moz-range-thumb { width: 14px; height: 14px; border-radius: 50%; background: var(--accent); border: none; }
  .share input:focus-visible { outline: 1px solid var(--accent); outline-offset: 5px; }

  /* the verdict */
  .link { border: none; background: none; padding: 0; cursor: pointer; font: inherit; font-size: var(--fs-small); font-weight: 600; color: var(--accent); }
  .link:disabled { color: var(--dim); cursor: default; }
  .eval p { margin: 0 0 6px; font-size: var(--fs-small); line-height: 1.5; }
  .bad { color: var(--err); }
  .warn { color: var(--warn); }
  .why { color: var(--muted); }
  .verdict { display: flex; align-items: baseline; gap: 8px; }
  .verdict b { font-size: var(--fs-body); font-weight: 640; }
  .verdict span { font-size: var(--fs-micro); color: var(--dim); }
  .verdict.fits b { color: var(--ok); }
  .verdict.tight b { color: var(--warn); }
  .verdict.no b { color: var(--err); }
  .verdict.unknown b { color: var(--muted); }
  table { width: 100%; border-collapse: collapse; margin-top: 8px; font-size: 10.5px; }
  th { text-align: right; font-weight: 500; color: var(--dim); padding: 0 0 4px; letter-spacing: .04em; }
  td { text-align: right; padding: 3px 0; color: var(--text); border-top: 1px solid var(--line); font-variant-numeric: tabular-nums; }
  th:first-child, td:first-child { text-align: left; color: var(--muted); }
  td.over { color: var(--err); }
  details { margin-top: 9px; }
  summary { cursor: pointer; font-size: var(--fs-small); color: var(--muted); }
  .chg { margin: 5px 0 0 !important; font-size: 10.5px !important; color: var(--muted); word-break: break-all; }

  .ro { display: flex; justify-content: space-between; padding: 6px 0; border-top: 1px solid var(--line); font-size: var(--fs-small); color: var(--text); }
  .ro:first-of-type { border-top: none; }
  .ro em { font-style: normal; font-size: var(--fs-micro); color: var(--dim); }

  .layout { display: flex; align-items: center; gap: 4px; }
  .lname {
    flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; text-align: left; cursor: pointer;
    padding: 6px 8px; margin-left: -8px; border: none; border-radius: 6px; background: transparent; font: inherit;
  }
  .lname:hover { background: var(--s2); }
  .lname b { font-size: var(--fs-small); font-weight: 600; color: var(--text); }
  .lname span { font-size: var(--fs-micro); color: var(--dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

  footer {
    position: sticky; bottom: 0; display: flex; gap: 8px; justify-content: flex-end; align-items: center;
    padding: 11px 16px; background: var(--s1); border-top: 1px solid var(--line);
  }
  footer button {
    cursor: pointer; font: inherit; font-size: var(--fs-small); font-weight: 600;
    padding: 7px 13px; border-radius: 7px; border: 1px solid transparent;
  }
  .ghost { background: none; color: var(--muted); border-color: var(--line2) !important; }
  .ghost:hover:not(:disabled) { color: var(--text); }
  .solid { background: var(--accent); color: var(--accent-ink); }
  .solid:hover:not(:disabled) { filter: brightness(1.08); }
  footer button:disabled { opacity: .4; cursor: default; }
  .nameit {
    flex: 1; min-width: 0; height: 32px; padding: 0 9px; border: none; border-radius: 7px;
    background: var(--bg); box-shadow: inset 0 0 0 1px var(--accent-line); color: var(--text); font: inherit; font-size: var(--fs-small);
  }
  .nameit:focus { outline: none; }

  @media (max-width: 1100px) { .form { width: 100%; max-height: none; } }
</style>
