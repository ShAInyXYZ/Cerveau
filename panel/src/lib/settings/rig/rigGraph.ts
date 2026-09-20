// The one place that decides what the Rig diagram draws. Pure: rig + live stats
// + health (+ the placement being edited) in, SvelteFlow nodes and edges out —
// so one card, five cards and no GPU at all are test cases, not things to eyeball.
//
// HARDWARE nodes are what the machine has. WORKLOAD nodes are what runs on it.
// A LINK says "runs on". The Core and the embedder are the two modules the
// user places; for them a link is in one of three states:
//   live     it is declared there AND running there now            (solid)
//   pending  it is declared there, and starts there on its next start (dashed)
//   leaving  it is running there, but no longer declared: gone after a restart (faded)
// Everything else found on the machine is `fixed`: drawn, not the user's to move.
import type { Health, Rig, RigConsumer, RigPlacement, SysStats } from '../../types';
import type { ArtKind, ArtTone } from './art/RigArt.svelte';
import { brandFor, type Brand } from '../../brands';

export type LinkState = 'live' | 'pending' | 'leaving' | 'fixed';
export type Movable = 'core' | 'embedder';
/** Where the two movable modules go, and how much of each card the Core
 *  takes. What the user edits, and what gets saved. */
export interface Placement { gpus: number[]; embed: { device: 'cuda' | 'cpu'; gpus: number[] }; share: number }
export type SegmentRole = 'core' | 'embedder' | 'other' | 'system';
export interface Segment { role: SegmentRole; mem: number; label: string; color: string } // MiB
export interface MeterReading { k: string; v: string; tone?: 'warm' | 'hot' }

export interface HardwareData extends Record<string, unknown> {
  kind: ArtKind;
  title: string;      // "GPU 2" · "CPU" · "RAM"
  name: string;       // "RTX 3090"
  facts: string;      // "24 GB · PCIe 4.0 ×16 · 250 W"
  load: number;       // 0–1, drives the illustration
  tone: ArtTone;
  total: number;      // MiB, the bar's full width; 0 = no bar
  segments: Segment[];
  meters: MeterReading[];
  accepts: Movable[]; // which modules may be dropped here
}
// preview: a profile that is not the loaded one — drawn where it WOULD go
export type WorkloadState = 'running' | 'parked' | 'down' | 'preview';
export interface WorkloadData extends Record<string, unknown> {
  role: 'core' | 'embedder' | 'crv' | 'typesense' | 'other';
  title: string;
  sub: string;
  state: WorkloadState;
  chips: string[];
  linkable: boolean;  // the user can drag a link from it
  /** whose mark to draw, resolved from the module's own strings; null = a neutral icon */
  brand: Brand | null;
  /** the module's colour — its card, its links and its share of a memory bar */
  color: string;
}
export interface RigNode {
  id: string; type: 'hardware' | 'workload';
  position: { x: number; y: number };
  data: HardwareData | WorkloadData;
  draggable: false; selectable: false; connectable: false;
}
export interface RigEdge {
  id: string; source: string; target: string;
  class: string; label?: string; style: string;
  selectable: false; deletable: false;
  /** unsaved: differs from what is saved on disk. removable: a click takes it away. */
  data: { state: LinkState; role: string; removable: boolean; unsaved: boolean };
}
export interface RigGraph {
  nodes: RigNode[]; edges: RigEdge[]; notes: string[];
  size: { w: number; h: number };  // the drawing's own box, so the frame can match its shape
  editable: boolean;               // the active profile has somewhere to write a placement
  restart: boolean;                // something saved is not running yet
}

export function placementOf(next: RigPlacement | null | undefined): Placement {
  const cuda = next?.embed.device === 'cuda';
  return {
    gpus: [...(next?.gpus ?? [])].sort((a, b) => a - b),
    embed: { device: cuda ? 'cuda' : 'cpu', gpus: cuda ? [...(next?.embed.gpus ?? [])] : [] },
    share: next?.gpu_util ?? 0,
  };
}
export function samePlacement(a: Placement, b: Placement): boolean {
  const key = (p: Placement) => `${[...p.gpus].sort((x, y) => x - y).join()}|${p.embed.device}|${p.embed.device === 'cuda' ? p.embed.gpus.join() : ''}|${Math.round(p.share * 100)}`;
  return key(a) === key(b);
}

// Card boxes, as the two node components draw them. Heights are the room a
// row is given, not a measurement: a GPU card with three owners runs tallest.
export const HW_W = 236;
export const WL_W = 236;
const GAP = 24;
const WL_H = 104;
const HW_H = 292;
const LIFT = 84;   // a workload row sits this far above its hardware: room for the edges
const BAND = 64;   // between the GPU band and the host band

/** MiB as GB: one decimal under 10 GB, none above. */
export const gb = (mib: number) => (mib / 1024).toFixed(mib >= 10240 ? 0 : 1);
/** "NVIDIA GeForce RTX 3090" is, on a card that says GPU, just "RTX 3090". */
export const shortGPU = (name: string) => name.replace(/^NVIDIA\s+/i, '').replace(/^GeForce\s+/i, '');

/** A placement in one line — the decision bar and the saved layouts say it the same way. */
export function describePlacement(p: { gpus: number[]; embed: { device: string; gpus?: number[] }; share?: number }): string {
  const core = p.gpus.length ? `GPU ${p.gpus.join(', ')}` : 'no card';
  const at = p.share ? ` at ${Math.round(p.share * 100)}%` : '';
  const embedder = p.embed.device === 'cuda' ? `GPU ${(p.embed.gpus ?? []).join(', ')}` : 'the CPU';
  return `Core on ${core}${at} · embedder on ${embedder}`;
}

/** The machine in one line, from what it reports about itself. */
export function machineLine(rig: Rig | null, stats: SysStats | null): string {
  const g = rig?.inventory.gpus ?? [];
  const ram = stats?.ram?.total;
  return [
    g.length ? `${g.length} GPU${g.length === 1 ? '' : 's'} · ${Math.round(g.reduce((a, c) => a + c.mem_total, 0) / 1024)} GB VRAM` : 'no GPU',
    // "… 16-Cores" is the vendor's marketing suffix; the thread count is on the CPU card
    stats?.cpu?.name?.replace(/\s+\d+-Cores?(\s+Processor)?$/i, '').trim(),
    ram ? `${Math.round(ram / 1024)} GB RAM` : '',
  ].filter(Boolean).join('  ·  ');
}
const tempTone = (t: number): MeterReading['tone'] => (t >= 85 ? 'hot' : t >= 72 ? 'warm' : undefined);

/** "docker-01234567…​.scope" → "docker 0123456789ab"; "foo.service" → "foo" */
export function unitLabel(unit: string | undefined, fallback: string): string {
  if (!unit) return fallback;
  const bare = unit.replace(/\.(service|scope)$/, '');
  const docker = bare.match(/^docker-([0-9a-f]{12})[0-9a-f]*$/);
  return docker ? `docker ${docker[1]}` : bare;
}

export function buildRigGraph(rig: Rig | null, stats: SysStats | null, health: Health | null, draft: Placement | null = null): RigGraph {
  const nodes: RigNode[] = [];
  const edges: RigEdge[] = [];
  const notes: string[] = [];
  if (!rig) return { nodes, edges, notes, size: { w: 0, h: 0 }, editable: false, restart: false };

  const gpus = rig.inventory.gpus ?? [];
  const live = new Map((stats?.gpus ?? []).map((g) => [g.index, g]));
  const use = new Map((rig.observed ?? []).map((u) => [u.gpu, u]));
  const next = rig.next;
  const comp = (name: string) => health?.components.find((c) => c.name === name);
  const profileName = (id: string) => rig.profiles?.find((p) => p.id === id)?.name ?? id;

  // ── hardware ──────────────────────────────────────────────────────────────
  const hw: { id: string; data: HardwareData }[] = [];
  for (const g of gpus) {
    const u = use.get(g.index);
    const l = live.get(g.index);
    const segments = cardSegments(u?.consumers ?? [], u?.used ?? 0);
    const busy = (u?.consumers ?? []).some((c) => c.role !== 'other');
    const facts = [`${gb(g.mem_total)} GB`];
    if (g.pcie_gen && g.pcie_width) facts.push(`PCIe ${g.pcie_gen}.0 ×${g.pcie_width}`);
    if (g.power_limit) facts.push(`${Math.round(g.power_limit)} W`);
    hw.push({
      id: `gpu-${g.index}`,
      data: {
        kind: 'gpu', title: `GPU ${g.index}`, name: shortGPU(g.name), facts: facts.join(' · '),
        load: l ? l.util / 100 : 0, tone: busy ? 'on' : 'idle',
        total: g.mem_total, segments, accepts: ['core', 'embedder'],
        meters: l ? [
          { k: 'temp', v: `${Math.round(l.temp)}°C`, tone: tempTone(l.temp) },
          { k: 'load', v: `${Math.round(l.util)}%` },
          { k: 'power', v: `${Math.round(l.power)} W` },
        ] : [],
      },
    });
  }
  const cpu = stats?.cpu;
  if (cpu) {
    hw.push({
      id: 'cpu',
      data: {
        kind: 'cpu', title: 'CPU', name: cpu.name, facts: `${cpu.cores} threads`,
        load: cpu.util / 100, tone: 'on', total: 0, segments: [], accepts: ['embedder'],
        meters: [
          ...(cpu.temp > 0 ? [{ k: 'temp', v: `${Math.round(cpu.temp)}°C`, tone: tempTone(cpu.temp) }] : []),
          { k: 'load', v: `${Math.round(cpu.util)}%` },
        ],
      },
    });
  }
  const ram = stats?.ram;
  if (ram && ram.total > 0) {
    const facts = [ram.type, ram.speed, ram.sticks ? `${ram.sticks} sticks` : ''].filter(Boolean).join(' · ');
    hw.push({
      id: 'ram',
      data: {
        kind: 'ram', title: 'RAM', name: `${gb(ram.total)} GB`, facts,
        load: ram.used / ram.total, tone: 'on', total: ram.total,
        segments: [{ role: 'system', mem: ram.used, label: `in use ${gb(ram.used)}`, color: 'var(--dim)' }],
        meters: [], accepts: [],
      },
    });
  }
  const has = (id: string) => hw.some((h) => h.id === id);

  // ── workloads, and the edges that place them ──────────────────────────────
  const wl: { id: string; data: WorkloadData }[] = [];
  // One colour per module, the same on its card, its links and its share of a
  // memory bar. The Core is the subject of this page and keeps the accent; the
  // rest take their brand's colour, and an unknown one stays grey.
  const colors = new Map<string, string>();
  const paint = (id: string, brand: Brand | null, fallback: string) => {
    const c = id === 'core' ? 'var(--accent)' : brand?.color ?? fallback;
    colors.set(id, c);
    return c;
  };

  const editable = !!next?.editable;
  const saved = placementOf(next);
  const want = draft ?? saved;          // what the diagram shows as declared
  let restart = false;

  // the class carries the link's state AND whose it is, so the Core's lines
  // can be told from a container's at a glance
  const link = (source: string, target: string, state: LinkState, o: { label?: string; removable?: boolean; unsaved?: boolean } = {}) => {
    if (!has(target)) return;
    const role = source.startsWith('other-') ? 'other' : source;
    const unsaved = !!o.unsaved;
    edges.push({
      id: `${source}->${target}`, source, target, label: o.label, style: `--link: ${colors.get(source) ?? 'var(--dim)'};`,
      class: `rig-edge ${state} ${role}${unsaved ? ' unsaved' : ''}${o.removable ? ' removable' : ''}`,
      selectable: false, deletable: false,
      data: { state, role, removable: !!o.removable, unsaved },
    });
  };
  // memory a role holds on each card, right now
  const held = (pick: (c: RigConsumer) => boolean) => {
    const m = new Map<number, number>();
    for (const [gpu, u] of use) {
      const sum = u.consumers.filter(pick).reduce((a, c) => a + c.mem, 0);
      if (sum > 0) m.set(gpu, sum);
    }
    return m;
  };
  // A movable module: one link per declared card, live or pending; and a
  // fading one for every card it still holds but has been moved off.
  const place = (id: Movable, on: Map<number, number>, declared: number[], before: number[]) => {
    for (const gpu of declared) {
      const live = on.has(gpu);
      link(id, `gpu-${gpu}`, live ? 'live' : 'pending', { label: live ? `${gb(on.get(gpu)!)} GB` : undefined, removable: editable, unsaved: !before.includes(gpu) });
    }
    for (const [gpu, mem] of on) {
      if (declared.includes(gpu)) continue;
      link(id, `gpu-${gpu}`, 'leaving', { label: `${gb(mem)} GB · until restart`, unsaved: before.includes(gpu) });
      if (!before.includes(gpu)) restart = true;   // saved, and still running the old way
    }
  };
  // everything else: where the driver sees it, and that is all
  const fix = (id: string, on: Map<number, number>) => {
    for (const [gpu, mem] of on) link(id, `gpu-${gpu}`, 'fixed', { label: `${gb(mem)} GB` });
  };

  if (next) {
    const on = held((c) => c.role === 'core' && c.core === next.core);
    // the model that answers is the LIVE profile's; a previewed one is not running
    const running = on.size > 0 || (next.live && !!comp('model')?.ok);
    const chips = [
      want.gpus.length ? `TP=${want.gpus.length}` : next.tp ? `TP=${next.tp}` : '',
      next.kv ? `${next.kv} KV` : '',
      next.max_len ? `${Math.round(next.max_len / 1024)}K window` : '',
      want.share || next.gpu_util ? `${Math.round((want.share || next.gpu_util!) * 100)}% of each card` : '',
    ].filter(Boolean);
    const engine = brandFor(next.engine);
    wl.push({ id: 'core', data: { role: 'core', title: next.name, sub: next.model ?? '', state: running ? 'running' : next.live ? 'parked' : 'preview', chips, linkable: editable, brand: engine, color: paint('core', engine, 'var(--accent)') } });
    place('core', on, want.gpus, saved.gpus);
    if (running && on.size > 0 && saved.gpus.some((g) => !on.has(g))) restart = true;
    if (!next.live) notes.push('This profile is not the loaded one: the dashed links are a preview of where it would go.');
    else if (!running && want.gpus.length) notes.push('The Core is parked. It loads onto the dashed cards when it next starts.');

    const eon = held((c) => c.role === 'embedder');
    const embed = comp('embedder');
    const onGPU = want.embed.device === 'cuda';
    // whose model it is, read off the model's own name ("nemotron-embed" → NVIDIA)
    const maker = brandFor(embed?.info, next.params?.EMBED_MODEL);
    wl.push({
      id: 'embedder',
      data: {
        role: 'embedder', title: 'Embedder', sub: embed?.info ?? 'recall embeddings',
        state: embed?.ok || eon.size > 0 ? 'running' : 'down',
        chips: [onGPU ? `GPU ${want.embed.gpus.join(', ')}` : 'CPU'], linkable: editable,
        brand: maker, color: paint('embedder', maker, 'var(--semantic)'),
      },
    });
    place('embedder', eon, onGPU ? want.embed.gpus : [], saved.embed.gpus);
    if (!onGPU) {
      // on the CPU there is no driver to ask: live when it answers and no card holds it
      const live = !!embed?.ok && eon.size === 0;
      link('embedder', 'cpu', live ? 'live' : 'pending', { removable: false, unsaved: saved.embed.device !== 'cpu' });
      if (saved.embed.device === 'cpu' && eon.size > 0) restart = true;
    } else if (saved.embed.device === 'cuda' && eon.size > 0 && saved.embed.gpus.some((g) => !eon.has(g))) restart = true;

    if (!editable) notes.push('This profile does not declare where it runs, so the diagram shows it and cannot move it.');
    if (!next.order_pinned && new Set(gpus.map((g) => g.name)).size > 1) {
      notes.push('Card order is not pinned yet: with mixed cards, the numbers CUDA uses are assumed to match the ones shown. Saving a placement pins it.');
    }
  }

  // a Core other than the selected one still holding cards, and everything else
  const strangers = new Map<string, { title: string; sub: string; unit: string; on: Map<number, number> }>();
  for (const [gpu, u] of use) {
    for (const c of u.consumers) {
      if (c.role === 'embedder' || (c.role === 'core' && c.core === next?.core)) continue;
      const key = c.role === 'core' ? `core:${c.core}` : `unit:${c.unit || c.name}`;
      const s = strangers.get(key) ?? { title: c.role === 'core' ? profileName(c.core!) : unitLabel(c.unit, c.name), sub: c.name, unit: c.unit ?? '', on: new Map() };
      s.on.set(gpu, (s.on.get(gpu) ?? 0) + c.mem);
      strangers.set(key, s);
    }
  }
  [...strangers.entries()].forEach(([key, s], i) => {
    const id = `other-${i}`;
    // the unit first: a container is Docker, whatever runs inside it
    const brand = key.startsWith('core:') ? brandFor(rig.profiles?.find((p) => p.name === s.title)?.engine) : brandFor(s.unit, s.title, s.sub);
    wl.push({ id, data: { role: 'other', title: s.title, sub: s.sub, state: 'running', chips: [key.startsWith('core:') ? 'the loaded Core' : 'not Cerveau'], linkable: false, brand, color: paint(id, brand, 'var(--dim)') } });
    fix(id, s.on);
  });

  // what Cerveau itself runs on the host
  if (has('cpu')) {
    const crv = { title: 'Cerveau', sub: health?.system?.version ? `crv ${health.system.version}` : 'crv · Go core' };
    const mark = brandFor(crv.title, crv.sub);   // from its own strings, like every other module
    wl.push({ id: 'crv', data: { role: 'crv', ...crv, state: 'running', chips: ['agent loop · tools · API'], linkable: false, brand: mark, color: paint('crv', mark, 'var(--muted)') } });
    link('crv', 'cpu', 'fixed');
  }
  const ts = comp('typesense');
  if (ts && has('ram')) {
    const mem = { title: 'Typesense', sub: ts.info ? `memory · ${ts.info}` : 'memory' };
    const mark = brandFor(ts.name, mem.title, mem.sub);
    wl.push({ id: 'typesense', data: { role: 'typesense', ...mem, state: ts.ok ? 'running' : 'down', chips: ['index held in RAM'], linkable: false, brand: mark, color: paint('typesense', mark, 'var(--muted)') } });
    link('typesense', 'ram', 'fixed');
  }

  if (!gpus.length) notes.push('No NVIDIA GPU detected — everything here runs on the CPU.');

  // A memory bar speaks the same colours as the cards above it: the embedder's
  // share is the embedder's colour, and a card held by one stranger takes that
  // stranger's — several strangers on one card stay grey, there is no one colour to give.
  for (const h of hw) {
    if (h.data.kind !== 'gpu') continue;
    const target = h.id;
    const guests = edges.filter((e) => e.target === target && e.data.role === 'other').map((e) => colors.get(e.source));
    for (const seg of h.data.segments) {
      if (seg.role === 'core') seg.color = colors.get('core') ?? seg.color;
      else if (seg.role === 'embedder') seg.color = colors.get('embedder') ?? seg.color;
      else if (seg.role === 'other' && guests.length === 1 && guests[0]) seg.color = guests[0];
    }
  }

  // ── layout: two bands, each a row of workloads over the hardware it runs on ─
  //
  //   Core · embedder · strangers        ← what runs on the cards
  //   GPU 0 · GPU 1 · …                  ← the cards
  //   Cerveau · Typesense                ← what runs on the host
  //   CPU · RAM                          ← the host
  //
  // One long row of seven cards was a thin strip lost in a tall canvas, and
  // every host edge had to cross the GPUs. Two bands fill the space and keep
  // each edge inside its own band.
  const isGPU = (id: string) => id.startsWith('gpu-');
  const onHost = (id: string) => {
    const targets = edges.filter((e) => e.source === id).map((e) => e.target);
    return targets.length > 0 && targets.every((t) => !isGPU(t));
  };
  const bands = [
    { hw: hw.filter((h) => isGPU(h.id)), wl: wl.filter((w) => !onHost(w.id)) },
    { hw: hw.filter((h) => !isGPU(h.id)), wl: wl.filter((w) => onHost(w.id)) },
  ].filter((b) => b.hw.length || b.wl.length);
  const rowW = (n: number, w: number) => Math.max(0, n * (w + GAP) - GAP);
  const full = Math.max(...bands.map((b) => Math.max(rowW(b.hw.length, HW_W), rowW(b.wl.length, WL_W))), 0);

  let y = 0;
  for (const b of bands) {
    // the hardware row, centred under the widest row of the whole graph
    const x0 = (full - rowW(b.hw.length, HW_W)) / 2;
    const centre = new Map(b.hw.map((h, i) => [h.id, x0 + i * (HW_W + GAP) + HW_W / 2]));
    const yHW = y + (b.wl.length ? WL_H + LIFT : 0);

    // each workload above the middle of what it runs on, then pushed apart
    const wanted = b.wl.map((w, order) => {
      const xs = edges.filter((e) => e.source === w.id && centre.has(e.target)).map((e) => centre.get(e.target)!);
      const x = xs.length ? xs.reduce((a, c) => a + c, 0) / xs.length - WL_W / 2 : Number.POSITIVE_INFINITY;
      return { ...w, x, order };
    }).sort((a, c) => a.x - c.x || a.order - c.order);
    let cursor = Number.NEGATIVE_INFINITY;
    for (const w of wanted) {
      const x = Math.max(Number.isFinite(w.x) ? w.x : cursor + WL_W + GAP, cursor + WL_W + GAP, 0);
      nodes.push(node(w.id, 'workload', x, y, w.data));
      cursor = x;
    }
    b.hw.forEach((h, i) => nodes.push(node(h.id, 'hardware', x0 + i * (HW_W + GAP), yHW, h.data)));
    y = yHW + (b.hw.length ? HW_H : 0) + BAND;
  }
  return { nodes, edges, notes, size: { w: full, h: Math.max(0, y - BAND) }, editable, restart: restart && !!next?.live };
}

function node(id: string, type: RigNode['type'], x: number, y: number, data: RigNode['data']): RigNode {
  return { id, type, position: { x, y }, data, draggable: false, selectable: false, connectable: false };
}

/** Split a card's memory by who holds it. What the driver counts beyond the
 *  compute processes is the desktop and the driver itself — real, and nobody's
 *  to move — so it is shown rather than folded into "free". */
const ROLE_COLOR: Record<SegmentRole, string> = { core: 'var(--accent)', embedder: 'var(--semantic)', other: 'var(--dim)', system: 'var(--faint)' };
function cardSegments(consumers: RigConsumer[], used: number): Segment[] {
  const by = new Map<SegmentRole, number>();
  for (const c of consumers) by.set(c.role, (by.get(c.role) ?? 0) + c.mem);
  const rest = used - consumers.reduce((a, c) => a + c.mem, 0);
  if (rest > 64) by.set('system', rest);
  const LABEL: Record<SegmentRole, string> = { core: 'Core', embedder: 'embedder', other: 'other', system: 'system' };
  return (['core', 'embedder', 'other', 'system'] as SegmentRole[])
    .filter((r) => (by.get(r) ?? 0) > 0)
    .map((r) => ({ role: r, mem: by.get(r)!, label: `${LABEL[r]} ${gb(by.get(r)!)}`, color: ROLE_COLOR[r] }));
}
