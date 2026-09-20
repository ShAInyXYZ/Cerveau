<script lang="ts">
  // Rig — the machine, and where everything runs on it.
  //
  // Pick a PROFILE at the top: the diagram draws it — in real time if it is the
  // loaded Core, as a preview of where it would go if it is not. Click a MODULE
  // to open its form: where it is connected, more, less, none, and whether the
  // result would run on this machine. The diagram redraws as the form changes,
  // and nothing is written until Save.
  //
  // rigGraph.ts decides what is drawn. /api/rig/plan decides whether a
  // placement is allowed, what saving it writes and whether it fits. This file
  // is the hands.
  import { onMount, untrack } from 'svelte';
  import { SvelteFlow, Background, Panel, type Connection } from '@xyflow/svelte';
  import '@xyflow/svelte/dist/style.css';
  import { RefreshCw } from 'lucide-svelte';
  import { api, ApiError } from '../../api';
  import type { RigPlan, RigSavedLayout } from '../../types';
  import { rigStore } from '../../stores/rig.svelte.ts';
  import { healthStore } from '../../stores/health.svelte.ts';
  import { buildRigGraph, placementOf, samePlacement, describePlacement, machineLine, type Placement, type RigEdge, type RigNode, type WorkloadData } from './rigGraph';
  import { rigLink } from './rigLink.svelte.ts';
  import HardwareNode from './HardwareNode.svelte';
  import WorkloadNode from './WorkloadNode.svelte';
  import ProfileBar from './ProfileBar.svelte';
  import ModuleForm from './ModuleForm.svelte';
  import RigFit from './RigFit.svelte';

  const nodeTypes = { hardware: HardwareNode, workload: WorkloadNode };
  // Fill the frame, but never blow a one-card machine up to poster size.
  const FIT = { padding: 0.03, maxZoom: 1.15 };

  // polled only while this section is on screen
  onMount(() => { rigStore.start(); return () => { rigStore.stop(); rigLink.end(); rigLink.close(); }; });

  // ── the placement being edited ────────────────────────────────────────────
  // null until the user changes something; then it is drawn instead of what is
  // saved. `plan` is the core's word on the placement on screen, edited or not.
  let draft = $state<Placement | null>(null);
  let plan = $state<RigPlan | null>(null);
  let busy = $state<'' | 'plan' | 'save' | 'restart'>('');
  let error = $state('');
  let saved = $state(false);          // something was just saved and not yet applied
  let conflict = $state('');          // the core refused a restart: runs in progress
  let layouts = $state<RigSavedLayout[]>([]);
  let seq = 0;

  const next = $derived(rigStore.rig?.next ?? null);
  const base = $derived(placementOf(next));
  const graph = $derived(buildRigGraph(rigStore.rig, rigStore.stats, healthStore.value, draft));
  let nodes = $state.raw<RigNode[]>([]);
  let edges = $state.raw<RigEdge[]>([]);
  $effect(() => { nodes = graph.nodes; edges = graph.edges; });

  async function check(p: Placement) {
    if (!next) return;
    busy = 'plan'; error = '';
    const mine = ++seq;
    try {
      const got = await api.rigPlan(next.core, p.gpus, p.embed, p.share);
      // dropped: a slower answer to an older change, or one for a profile no longer on screen
      if (mine === seq && got.core === rigStore.rig?.next?.core) plan = got;
    } catch (e) {
      if (mine === seq) error = e instanceof Error ? e.message : String(e);
    } finally { if (mine === seq) busy = ''; }
  }
  function edit(to: Placement) {
    saved = false; conflict = '';
    draft = samePlacement(to, base) ? null : to;
    plan = null;
    void check(to);
  }
  function discard() { seq++; draft = null; plan = null; error = ''; busy = ''; if (rigLink.selected) void check(base); }

  // ── profiles ──
  async function pick(id: string) {
    if (id === next?.core) return;
    seq++;                 // whatever is in flight belongs to the profile being left
    await rigStore.select(id);
  }
  // the layouts kept beside whichever profile is on screen
  $effect(() => {
    const id = next?.core;
    layouts = [];
    if (id) void api.rigLayouts(id).then((l) => { if (id === rigStore.rig?.next?.core) layouts = l; });
  });

  // ── the module form ──
  // When the profile on screen changes, nothing computed for the old one may
  // outlive it. That happens by the user's pick — and ALSO with no user action
  // at all: with no profile picked the page follows the live Core, and another
  // device (or the CLI) can switch that under an open page. A draft made for
  // profile A, saved after the poll turned the page into profile B, would
  // overwrite B's overrides with A's cards.
  //
  // The same moment opens the engine card's form: it is what this page is for,
  // and a diagram with nothing selected gave no hint that anything could be
  // changed. Once per profile — closing it stays closed through the polls.
  let shownCore = '';
  $effect(() => {
    const id = next?.core ?? '';
    if (!id || id === shownCore || !graph.nodes.some((n) => n.id === 'core')) return;
    shownCore = id;
    untrack(() => {
      seq++; draft = null; plan = null; saved = false; conflict = ''; error = ''; busy = '';
      rigLink.open('core');
      if (next?.editable) void check(placementOf(next));
    });
  });

  const selected = $derived(graph.nodes.find((n) => n.id === rigLink.selected && n.type === 'workload') ?? null);
  function openModule({ node }: { node: RigNode }) {
    if (node.type !== 'workload') return;
    rigLink.select(node.id);
    if (rigLink.selected && !plan && next?.editable) void check(draft ?? base);
  }

  // ── linking on the diagram itself ──
  const cardOf = (id: string | null | undefined) => (id?.startsWith('gpu-') ? Number(id.slice(4)) : null);
  const valid = (c: Connection | RigEdge) =>
    (c.source === 'core' && cardOf(c.target) !== null) ||
    (c.source === 'embedder' && (cardOf(c.target) !== null || c.target === 'cpu'));
  // SvelteFlow would add its own edge on a connect. Ours are derived from the
  // placement, so the link is taken here and the built-in one declined.
  function connect(c: Connection) {
    const now = draft ?? base;
    const card = cardOf(c.target);
    if (c.source === 'core' && card !== null && !now.gpus.includes(card)) {
      edit({ ...now, gpus: [...now.gpus, card].sort((a, b) => a - b) });
    } else if (c.source === 'embedder') {
      edit({ ...now, embed: card !== null ? { device: 'cuda', gpus: [card] } : { device: 'cpu', gpus: [] } });
    }
    return undefined;
  }
  // Taking the embedder off its card does not leave it nowhere: it runs on the CPU.
  function unlink({ edge }: { edge: RigEdge }) {
    if (!edge.data.removable) return;
    const now = draft ?? base;
    const card = cardOf(edge.target);
    if (edge.source === 'core' && card !== null) edit({ ...now, gpus: now.gpus.filter((g) => g !== card) });
    else if (edge.source === 'embedder' && card !== null) edit({ ...now, embed: { device: 'cpu', gpus: [] } });
  }

  // ── save · save as · restart ──
  async function save() {
    if (!plan || !next || plan.problems.length) return;
    // belt and braces: a plan is saved to the profile it was made for, or not at all
    if (plan.core !== next.core) { plan = null; void check(draft ?? base); return; }
    busy = 'save'; error = '';
    try {
      await api.savePlacement(plan.core, plan);
      await rigStore.refresh();
      seq++; draft = null; saved = true;
      if (rigLink.selected) void check(placementOf(rigStore.rig?.next)); else plan = null;
    } catch (e) { error = e instanceof Error ? e.message : String(e); }
    finally { if (busy === 'save') busy = ''; }
  }
  async function saveAs(name: string) {
    if (!next) return;
    const p = draft ?? base;
    try { layouts = await api.saveRigLayout(next.core, { name, gpus: p.gpus, embed: p.embed, gpu_util: p.share || undefined }); }
    catch (e) { error = e instanceof Error ? e.message : String(e); }
  }
  async function forget(name: string) {
    if (!next) return;
    try { layouts = await api.deleteRigLayout(next.core, name); }
    catch (e) { error = e instanceof Error ? e.message : String(e); }
  }
  // loading a layout only fills the editor: the diagram previews it, Save writes it
  function load(l: RigSavedLayout) {
    const cuda = l.embed.device === 'cuda';
    edit({ gpus: [...l.gpus], embed: { device: cuda ? 'cuda' : 'cpu', gpus: cuda ? [...(l.embed.gpus ?? [])] : [] }, share: l.gpu_util ?? base.share });
  }
  async function restart(force = false) {
    if (!next) return;
    busy = 'restart'; error = ''; conflict = '';
    try {
      await api.applyCore(next.core, force);
      saved = false;
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) conflict = e.message;
      else error = e instanceof Error ? e.message : String(e);
    } finally { busy = ''; }
  }

  // the diagram re-frames when its shape or its frame changes — never on a meter tick
  let frame: HTMLElement | undefined = $state();
  let size = $state('');
  $effect(() => {
    if (!frame) return;
    const ro = new ResizeObserver(([e]) => { size = `${Math.round(e.contentRect.width / 40)}x${Math.round(e.contentRect.height / 40)}`; });
    ro.observe(frame);
    return () => ro.disconnect();
  });
  const shape = $derived(`${graph.nodes.map((n) => n.id).join()}|${size}`);

  const machine = $derived(machineLine(rigStore.rig, rigStore.stats));
  const applyLabel = $derived(next?.live ? 'Restart Core' : `Switch to ${next?.name ?? 'this profile'}`);
</script>

<section class="rig">
  {#if rigStore.phase === 'ready' && rigStore.rig?.profiles?.length}
    <ProfileBar profiles={rigStore.rig.profiles} {next} {machine} notes={graph.notes} onpick={pick} />
  {:else if rigStore.phase === 'ready' && graph.notes.length}
    <ul class="notes">{#each graph.notes as n (n)}<li>{n}</li>{/each}</ul>
  {/if}

  <!-- One bar, and only when there is a decision the form is not already
       showing: an unsaved placement made by dragging, a saved one waiting to
       be loaded, or a refused restart. -->
  {#if draft && !selected}
    <div class="bar unsaved" role="status">
      <div class="bar-text">
        <b>Unsaved placement</b><span>{describePlacement(draft)}</span>
        {#if busy === 'plan'}<span class="quiet">checking…</span>{/if}
        {#each plan?.problems ?? [] as p (p)}<span class="bad">{p}</span>{/each}
        {#each plan?.warnings ?? [] as w (w)}<span class="warn">{w}</span>{/each}
        {#if plan?.fit && !plan.problems.length}<span class="quiet">{plan.fit.reasons[0] ?? ''}</span>{/if}
      </div>
      <div class="bar-actions">
        <button class="ghost" onclick={discard}>Cancel</button>
        <button class="solid" disabled={!plan || plan.problems.length > 0 || busy !== ''} onclick={save}>
          {busy === 'save' ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  {:else if conflict}
    <div class="bar" role="alert">
      <div class="bar-text"><b>Not restarted</b><span>{conflict}.</span></div>
      <div class="bar-actions">
        <button class="ghost" onclick={() => (conflict = '')}>Wait</button>
        <button class="solid danger" disabled={busy !== ''} onclick={() => restart(true)}>Restart anyway</button>
      </div>
    </div>
  {:else if !draft && (saved || graph.restart)}
    <div class="bar" role="status">
      <div class="bar-text">
        <b>{saved ? 'Saved' : 'Saved placement not running yet'}</b>
        <span>
          {next?.live
            ? 'It takes effect when the Core restarts — that reloads the model and restarts Cerveau.'
            : 'It takes effect when this profile is loaded — that parks the current Core and restarts Cerveau.'}
        </span>
      </div>
      <div class="bar-actions">
        <button class="solid" disabled={busy !== ''} onclick={() => restart()}>
          <RefreshCw size={13} />{busy === 'restart' ? 'Asking…' : applyLabel}
        </button>
      </div>
    </div>
  {/if}
  {#if error}<p class="alert" role="alert">{error}</p>{/if}

  {#if rigStore.phase === 'unsupported'}
    <p class="empty mono">
      This core does not serve /api/rig yet — it is older than the panel. Rebuild and restart crv to see the machine here.
    </p>
  {:else if rigStore.phase === 'loading'}
    <p class="empty mono">reading the machine…</p>
  {:else if nodes.length}
    <div class="stage">
      <div class="main">
        <!-- The frame takes the drawing's own proportions, so there is no
             empty canvas around it. -->
        <div class="frame" bind:this={frame} style="aspect-ratio: {graph.size.w} / {graph.size.h}"
          class:linking={rigLink.from !== null}>
          <SvelteFlow bind:nodes bind:edges {nodeTypes} fitView fitViewOptions={FIT}
            colorMode="dark" minZoom={0.2} maxZoom={1.15}
            panOnDrag={false} panOnScroll={false} zoomOnScroll={false} zoomOnPinch={false} zoomOnDoubleClick={false}
            preventScrolling={false}
            nodesDraggable={false} elementsSelectable={false} nodesConnectable={graph.editable}
            connectionRadius={60} isValidConnection={valid} onbeforeconnect={connect}
            onconnectstart={(_, p) => rigLink.start(p.nodeId)} onconnectend={() => rigLink.end()}
            onedgeclick={unlink} onnodeclick={openModule} onpaneclick={() => rigLink.close()}
            connectionLineStyle="stroke: var(--accent); stroke-width: 2; stroke-dasharray: 7 6;"
            proOptions={{ hideAttribution: true }}>
            <Background bgColor="var(--bg)" patternColor="var(--line)" gap={26} />
            <RigFit {shape} options={FIT} />
            <!-- a map's legend lives on the map, in the corner the drawing leaves empty -->
            <Panel position="bottom-left">
              <div class="key mono">
                <span><svg width="26" height="6" aria-hidden="true"><line x1="0" y1="3" x2="26" y2="3" /></svg>running here</span>
                <span><svg width="26" height="6" class="dash" aria-hidden="true"><line x1="0" y1="3" x2="26" y2="3" /></svg>{next?.live ? 'after a restart' : 'where it would go'}</span>
                <span><svg width="26" height="6" class="fade" aria-hidden="true"><line x1="0" y1="3" x2="26" y2="3" /></svg>leaving after a restart</span>
              </div>
            </Panel>
          </SvelteFlow>
        </div>
      </div>

      {#if selected}
        <ModuleForm id={selected.id} module={selected.data as WorkloadData}
          placement={draft ?? base} gpus={rigStore.rig?.inventory.gpus ?? []} observed={rigStore.rig?.observed ?? []}
          links={graph.edges.filter((e) => e.source === selected.id)}
          {plan} checking={busy === 'plan'} dirty={!!draft} editable={graph.editable}
          hasCPU={graph.nodes.some((n) => n.id === 'cpu')} {layouts} saving={busy === 'save'}
          onedit={edit} onevaluate={() => check(draft ?? base)}
          onsave={save} onsaveas={saveAs} oncancel={discard} onclose={() => rigLink.close()}
          onload={load} onforget={forget} />
      {/if}
    </div>
  {/if}
</section>

<style>
  .rig { display: flex; flex-direction: column; min-height: 0; }
  /* the diagram, and beside it the form of whichever module is open — tops aligned */
  .stage { display: flex; gap: 16px; align-items: flex-start; margin-top: 14px; }
  .main { flex: 1; min-width: 0; }

  /* the legend, on the map */
  .key {
    display: flex; flex-direction: column; gap: 5px; padding: 9px 11px; border-radius: 8px;
    background: color-mix(in srgb, var(--s1) 92%, transparent); box-shadow: 0 0 0 1px var(--line);
    font-size: var(--fs-micro); letter-spacing: .06em; color: var(--muted); pointer-events: none;
  }
  .key span { display: inline-flex; align-items: center; gap: 8px; }
  .key line { stroke: var(--text); stroke-width: 2; }
  .key .dash line { stroke-dasharray: 7 6; }
  .key .fade line { opacity: .35; }

  /* only when there is no profile to hang them under */
  .notes { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 3px; }
  .notes li { position: relative; padding-left: 14px; font-size: var(--fs-small); line-height: 1.5; color: var(--muted); }
  .notes li::before { content: ''; position: absolute; left: 2px; top: .62em; width: 5px; height: 5px; border-radius: 50%; background: var(--warn); }

  /* the one decision on screen */
  .bar {
    display: flex; align-items: center; gap: 16px; flex-wrap: wrap;
    margin-top: 12px; padding: 10px 12px 10px 14px; border-radius: 8px;
    background: var(--s2); box-shadow: inset 0 0 0 1px var(--line2);
  }
  .bar.unsaved { box-shadow: inset 0 0 0 1px var(--accent-line); }
  .bar-text { flex: 1; min-width: 240px; display: flex; flex-wrap: wrap; align-items: baseline; gap: 3px 12px; font-size: var(--fs-small); color: var(--muted); line-height: 1.5; }
  .bar-text b { color: var(--text); font-weight: 620; }
  .bar-text .quiet { color: var(--dim); flex-basis: 100%; }
  .bar-text .bad { color: var(--err); flex-basis: 100%; }
  .bar-text .warn { color: var(--warn); flex-basis: 100%; }
  .bar-actions { display: flex; gap: 8px; flex-shrink: 0; }
  .bar button {
    display: inline-flex; align-items: center; gap: 6px; cursor: pointer;
    font: inherit; font-size: var(--fs-small); font-weight: 600;
    padding: 7px 13px; border-radius: 7px; border: 1px solid transparent;
    transition: background var(--t-fast), color var(--t-fast), border-color var(--t-fast);
  }
  .ghost { background: none; color: var(--muted); border-color: var(--line2) !important; }
  .ghost:hover { color: var(--text); }
  .solid { background: var(--accent); color: var(--accent-ink); }
  .solid:hover:not(:disabled) { filter: brightness(1.08); }
  .solid.danger { background: none; color: var(--err); border-color: var(--err) !important; }
  .bar button:disabled { opacity: .45; cursor: default; }

  /* Shaped like the drawing; never taller than the window leaves room for. */
  .frame {
    width: 100%; max-height: calc(100vh - 250px); min-height: 380px;
    border-radius: 10px; overflow: hidden; box-shadow: 0 0 0 1px var(--line);
  }
  .frame :global(.svelte-flow) {
    background: var(--bg);
    --xy-edge-label-background-color: var(--bg);
    --xy-edge-label-color: var(--text);
  }
  /* nothing to pan: the grab cursor promised something the diagram does not do */
  .frame :global(.svelte-flow__pane) { cursor: default; }
  .frame :global(.svelte-flow__edge-label) { font-family: var(--font-mono); font-size: 11px; padding: 2px 5px; border-radius: 3px; pointer-events: none; }

  /* A link's colour is its module's (--link, set on each path from its brand) —
     the same as the module's card and its share of a memory bar.
     Its stroke says its state. 2px, or they vanish when the diagram scales down. */
  .frame :global(.rig-edge .svelte-flow__edge-path) { stroke: var(--link, var(--dim)); stroke-width: 2; transition: stroke-width var(--t-fast), opacity var(--t-fast); }
  .frame :global(.rig-edge.pending .svelte-flow__edge-path) { stroke-dasharray: 7 6; }
  .frame :global(.rig-edge.leaving .svelte-flow__edge-path) { opacity: .35; }
  .frame :global(.rig-edge.fixed .svelte-flow__edge-path) { stroke-width: 1.5; }
  /* not on disk yet: drawn heavier, so an edit is never mistaken for a fact */
  .frame :global(.rig-edge.unsaved .svelte-flow__edge-path) { stroke-width: 3; }
  /* a link that a click removes says so under the pointer */
  .frame :global(.rig-edge.removable) { cursor: pointer; }
  .frame :global(.rig-edge.removable:hover .svelte-flow__edge-path) { stroke: var(--err); stroke-width: 3; stroke-dasharray: 3 5; }
  /* while a new link is in the air, the existing ones step back */
  .frame.linking :global(.rig-edge .svelte-flow__edge-path) { opacity: .25; }

  @media (max-width: 1100px) { .stage { flex-direction: column; } }
</style>
