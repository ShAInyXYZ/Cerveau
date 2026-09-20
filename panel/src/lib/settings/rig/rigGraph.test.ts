import { describe, it, expect } from 'vitest';
import { buildRigGraph, placementOf, samePlacement, describePlacement, machineLine, unitLabel, HW_W, WL_W, type HardwareData, type WorkloadData } from './rigGraph';
import type { Health, Rig, RigCardUse, RigGPU, SysStats } from '../../types';

// A five-card rig in the shape GET /api/rig reports it: four 3090s, a 3060, a
// TP=4 Core placed on 0–3 and the embedder on 4. Identifiers are made up.
const card = (index: number, name: string, mem: number, w: number): RigGPU =>
  ({ index, uuid: `GPU-${index}`, name, mem_total: mem, pcie_gen: 4, pcie_width: 16, power_limit: w, compute_cap: '8.6' });
const GPUS = [0, 1, 2, 3].map((i) => card(i, 'NVIDIA GeForce RTX 3090', 24576, 250)).concat(card(4, 'NVIDIA GeForce RTX 3060', 12288, 190));
const DOCKER = 'docker-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.scope';
const docker = (mem: number) => ({ pid: 4242, mem, name: 'python3', unit: DOCKER, role: 'other' as const });

function labRig(observed: RigCardUse[], over: Partial<NonNullable<Rig['next']>> = {}): Rig {
  return {
    inventory: { gpus: GPUS, nvlink: false },
    observed,
    next: {
      core: 'vllm-27b-w8a16', name: 'vLLM · W8A16 · TP=4', engine: 'vLLM', model: 'Qwen3.8-27B W8A16 + MTP',
      gpus: [0, 1, 2, 3], tp: 4, gpu_util: 0.92, kv: 'fp8', max_len: 262144,
      embed: { device: 'cuda', gpus: [4] }, editable: true, overridden: false, order_pinned: false, params: null, live: true, ...over,
    },
  };
}
const PARKED: RigCardUse[] = [
  ...[0, 1, 2, 3].map((gpu): RigCardUse => ({ gpu, used: 280, consumers: [docker(256)] })),
  { gpu: 4, used: 3598, consumers: [docker(104)] },
];
const worker = (mem: number) => ({ pid: 100, mem, name: 'VLLM::Worker', unit: 'crv-core-vllm-27b-w8a16.service', role: 'core' as const, core: 'vllm-27b-w8a16' });
const RUNNING: RigCardUse[] = [
  ...[0, 1, 2, 3].map((gpu): RigCardUse => ({ gpu, used: 22300, consumers: [worker(22000)] })),
  { gpu: 4, used: 5100, consumers: [{ pid: 200, mem: 1500, name: 'python3', unit: 'cerveau-embed.service', role: 'embedder' }] },
];

const STATS: SysStats = {
  gpus: GPUS.map((g) => ({ index: g.index, name: g.name, temp: 41, util: g.index === 2 ? 35 : 0, mem_used: 280, mem_total: g.mem_total, power: 24, power_max: 250, fan: 0 })),
  cpu: { name: 'AMD Ryzen 9 7950X 16-Core Processor', cores: 32, temp: 48, util: 6 },
  ram: { used: 20480, total: 65536, type: 'DDR5', speed: '5600 MT/s', sticks: 2 },
};
const health = (model: boolean): Health => ({
  components: [{ name: 'model', ok: model }, { name: 'embedder', ok: true, info: 'nemotron-3-embed-1b' }, { name: 'typesense', ok: true, info: '27.1' }],
  system: { version: '0.6.0-alpha' },
});

const ids = (g: ReturnType<typeof buildRigGraph>, type: string) => g.nodes.filter((n) => n.type === type).map((n) => n.id);
const edge = (g: ReturnType<typeof buildRigGraph>, source: string) => g.edges.filter((e) => e.source === source);
const links = (g: ReturnType<typeof buildRigGraph>, source: string) => edge(g, source).map((e) => `${e.target}:${e.data.state}`);
const node = <T,>(g: ReturnType<typeof buildRigGraph>, id: string) => g.nodes.find((n) => n.id === id)!.data as T;

describe('buildRigGraph', () => {
  it('draws one hardware card per detected GPU, then the CPU and the RAM', () => {
    const g = buildRigGraph(labRig(PARKED), STATS, health(false));
    expect(ids(g, 'hardware')).toEqual(['gpu-0', 'gpu-1', 'gpu-2', 'gpu-3', 'gpu-4', 'cpu', 'ram']);
    expect(node<HardwareData>(g, 'gpu-2')).toMatchObject({ kind: 'gpu', title: 'GPU 2', name: 'RTX 3090', facts: '24 GB · PCIe 4.0 ×16 · 250 W', load: 0.35 });
    expect(node<HardwareData>(g, 'gpu-4').name).toBe('RTX 3060');
  });

  // The state the rig was actually in: the Core parked, a docker container
  // holding a little memory on every card, the desktop on the 3060.
  it('shows a parked Core as pending links, and says so', () => {
    const g = buildRigGraph(labRig(PARKED), STATS, health(false));
    const core = node<WorkloadData>(g, 'core');
    expect(core.state).toBe('parked');
    expect(core.chips).toEqual(['TP=4', 'fp8 KV', '256K window', '92% of each card']);
    expect(links(g, 'core')).toEqual(['gpu-0:pending', 'gpu-1:pending', 'gpu-2:pending', 'gpu-3:pending']);
    // the dashes and the legend say "after a restart"; a label on every line was noise
    expect(edge(g, 'core').every((e) => e.label === undefined)).toBe(true);
    expect(g.notes[0]).toMatch(/parked/);
    expect(g.restart).toBe(false);
    // nothing of Cerveau's is on the cards, so none of them reads as in use
    expect(node<HardwareData>(g, 'gpu-0').tone).toBe('idle');
  });

  it('attributes what is on the cards: the container is a workload, the desktop is "system"', () => {
    const g = buildRigGraph(labRig(PARKED), STATS, health(false));
    expect(node<WorkloadData>(g, 'other-0')).toMatchObject({ role: 'other', title: 'docker 0123456789ab', sub: 'python3', linkable: false });
    expect(edge(g, 'other-0')).toHaveLength(5);
    expect(edge(g, 'other-0')[0]).toMatchObject({ target: 'gpu-0', label: '0.3 GB', data: { state: 'fixed', removable: false } });
    expect(node<HardwareData>(g, 'gpu-4').segments.map((s) => [s.role, s.mem])).toEqual([['other', 104], ['system', 3494]]);
  });

  it('draws a running Core as live links, with what it holds on each card', () => {
    const g = buildRigGraph(labRig(RUNNING), STATS, health(true));
    expect(node<WorkloadData>(g, 'core').state).toBe('running');
    expect(edge(g, 'core').every((e) => e.data.state === 'live' && e.label === '21 GB')).toBe(true);
    expect(edge(g, 'embedder').map((e) => [e.target, e.data.state, e.label])).toEqual([['gpu-4', 'live', '1.5 GB']]);
    expect(node<HardwareData>(g, 'gpu-0').tone).toBe('on');
    expect(g.restart).toBe(false);
  });

  // The user saved "pack onto cards 2 and 3" and has not restarted: the Core
  // stays live on 2 and 3, is LEAVING 0 and 1, and the diagram asks for the restart.
  it('shows a saved move that is not running yet: leaving links and a restart', () => {
    const g = buildRigGraph(labRig(RUNNING, { gpus: [2, 3], tp: 2, overridden: true }), STATS, health(true));
    expect(links(g, 'core')).toEqual(['gpu-2:live', 'gpu-3:live', 'gpu-0:leaving', 'gpu-1:leaving']);
    expect(edge(g, 'core')[2].label).toBe('21 GB · until restart');
    expect(g.restart).toBe(true);
    // and the other way round: running on 2–3, saved back to all four
    const back = buildRigGraph(labRig(RUNNING.slice(2)), STATS, health(true));
    expect(links(back, 'core')).toEqual(['gpu-0:pending', 'gpu-1:pending', 'gpu-2:live', 'gpu-3:live']);
    expect(back.restart).toBe(true);
  });

  // The user drags the Core off 0 and 1 and has NOT saved. Same picture, but
  // the changed links are marked unsaved, and no restart is asked for yet —
  // there is nothing on disk to apply.
  it('draws the placement being edited, and marks what differs from disk', () => {
    const rig = labRig(RUNNING);
    const g = buildRigGraph(rig, STATS, health(true), { gpus: [2, 3], embed: { device: 'cuda', gpus: [4] }, share: 0.92 });
    expect(links(g, 'core')).toEqual(['gpu-2:live', 'gpu-3:live', 'gpu-0:leaving', 'gpu-1:leaving']);
    expect(edge(g, 'core').map((e) => e.data.unsaved)).toEqual([false, false, true, true]);
    expect(node<WorkloadData>(g, 'core').chips[0]).toBe('TP=2');
    expect(g.restart).toBe(false);
    // adding a card the Core does not hold yet: pending, unsaved, and a click removes it
    const parked = buildRigGraph(labRig(PARKED, { gpus: [0, 1] }), STATS, health(false), { gpus: [0, 1, 2, 3], embed: { device: 'cuda', gpus: [4] }, share: 0.92 });
    expect(edge(parked, 'core').map((e) => [e.target, e.data.state, e.data.unsaved, e.data.removable]))
      .toEqual([['gpu-0', 'pending', false, true], ['gpu-1', 'pending', false, true], ['gpu-2', 'pending', true, true], ['gpu-3', 'pending', true, true]]);
  });

  // The user picks the BF16 profile while W8A16 is the one loaded: the diagram
  // previews BF16 — all dashed, no restart asked — and the loaded Core is still
  // on the cards, named, as something that is there.
  it('previews a profile that is not loaded, beside the Core that is', () => {
    const rig = labRig(RUNNING, { core: 'vllm-27b-bf16', name: 'vLLM · BF16 · TP=4', live: false });
    rig.profiles = [{ id: 'vllm-27b-w8a16', name: 'vLLM · W8A16 · TP=4', engine: 'vLLM', live: true, editable: true }];
    const g = buildRigGraph(rig, STATS, health(true));
    expect(node<WorkloadData>(g, 'core')).toMatchObject({ title: 'vLLM · BF16 · TP=4', state: 'preview' });
    expect(links(g, 'core')).toEqual(['gpu-0:pending', 'gpu-1:pending', 'gpu-2:pending', 'gpu-3:pending']);
    expect(node<WorkloadData>(g, 'other-0')).toMatchObject({ title: 'vLLM · W8A16 · TP=4', chips: ['the loaded Core'] });
    expect(g.restart).toBe(false);
    expect(g.notes.some((n) => /preview/.test(n))).toBe(true);
  });

  it('shows the share of each card being edited', () => {
    const g = buildRigGraph(labRig(PARKED), STATS, health(false), { gpus: [0, 1, 2, 3], embed: { device: 'cuda', gpus: [4] }, share: 0.8 });
    expect(node<WorkloadData>(g, 'core').chips).toContain('80% of each card');
  });

  // "Proper icons and colours", resolved from each module's own strings — the
  // panel names no brand. One colour per module: card, links, and its bar share.
  it('gives every module its brand and one colour, used everywhere', () => {
    const g = buildRigGraph(labRig(RUNNING), STATS, health(true));
    expect(node<WorkloadData>(g, 'core')).toMatchObject({ color: 'var(--accent)' });
    expect(node<WorkloadData>(g, 'core').brand?.id).toBe('vllm');
    const embedder = node<WorkloadData>(g, 'embedder');
    expect(embedder.brand?.id).toBe('nvidia');          // from "nemotron-embed"
    expect(edge(g, 'embedder')[0].style).toBe(`--link: ${embedder.color};`);
    expect(node<HardwareData>(g, 'gpu-4').segments.find((s) => s.role === 'embedder')!.color).toBe(embedder.color);
    expect(node<WorkloadData>(g, 'crv').brand?.id).toBe('cerveau');
    expect(node<WorkloadData>(g, 'typesense').brand?.id).toBe('typesense');

    // the container on every card: Docker's mark and colour, down to the bars
    const parked = buildRigGraph(labRig(PARKED), STATS, health(false));
    const docker = node<WorkloadData>(parked, 'other-0');
    expect(docker.brand?.id).toBe('docker');
    expect(node<HardwareData>(parked, 'gpu-0').segments.find((s) => s.role === 'other')!.color).toBe(docker.color);
  });

  it('says what may be dropped where: both modules on a card, only the embedder on the CPU', () => {
    const g = buildRigGraph(labRig(PARKED), STATS, health(false));
    expect(node<HardwareData>(g, 'gpu-0').accepts).toEqual(['core', 'embedder']);
    expect(node<HardwareData>(g, 'cpu').accepts).toEqual(['embedder']);
    expect(node<HardwareData>(g, 'ram').accepts).toEqual([]);
    expect(node<WorkloadData>(g, 'core').linkable).toBe(true);
    expect(node<WorkloadData>(g, 'crv').linkable).toBe(false);
    expect(g.editable).toBe(true);
  });

  // llama.cpp and the single-card vLLM Core declare no placement keys: drawn, never editable.
  it('draws a profile it cannot move, and says why', () => {
    const g = buildRigGraph(labRig(PARKED, { editable: false }), STATS, health(false));
    expect(g.editable).toBe(false);
    expect(node<WorkloadData>(g, 'core').linkable).toBe(false);
    expect(edge(g, 'core').every((e) => !e.data.removable)).toBe(true);
    expect(g.notes.some((n) => /cannot move it/.test(n))).toBe(true);
  });

  it('puts the embedder on the CPU when the profile gives it no card', () => {
    const g = buildRigGraph(labRig(PARKED, { embed: { device: 'cpu' } }), STATS, health(false));
    expect(links(g, 'embedder')).toEqual(['cpu:live']);
    expect(node<WorkloadData>(g, 'embedder').chips).toEqual(['CPU']);
    // moved to the CPU in the editor while it still holds the 3060: pending there, leaving here
    const moving = buildRigGraph(labRig(RUNNING), STATS, health(true), { gpus: [0, 1, 2, 3], embed: { device: 'cpu', gpus: [] }, share: 0.92 });
    expect(links(moving, 'embedder')).toEqual(['gpu-4:leaving', 'cpu:pending']);
  });

  it('places Cerveau and Typesense on the host', () => {
    const g = buildRigGraph(labRig(PARKED), STATS, health(false));
    expect(links(g, 'crv')).toEqual(['cpu:fixed']);
    expect(links(g, 'typesense')).toEqual(['ram:fixed']);
  });

  it('warns that card numbers are assumed only when the cards differ and the order is unpinned', () => {
    const has = (g: ReturnType<typeof buildRigGraph>) => g.notes.some((n) => /not pinned/.test(n));
    expect(has(buildRigGraph(labRig(PARKED), STATS, health(false)))).toBe(true);
    expect(has(buildRigGraph(labRig(PARKED, { order_pinned: true }), STATS, health(false)))).toBe(false);
    const same: Rig = { ...labRig([]), inventory: { gpus: GPUS.slice(0, 4), nvlink: false } };
    expect(has(buildRigGraph(same, STATS, health(false)))).toBe(false);
  });

  it('draws a one-card machine, and a machine with no GPU at all', () => {
    const one: Rig = { inventory: { gpus: GPUS.slice(0, 1), nvlink: false }, observed: [], next: { ...labRig([]).next!, gpus: [0], tp: 1, embed: { device: 'cpu' } } };
    expect(ids(buildRigGraph(one, STATS, health(false)), 'hardware')).toEqual(['gpu-0', 'cpu', 'ram']);

    const none: Rig = { inventory: { gpus: null, nvlink: false }, observed: null, next: null };
    const g = buildRigGraph(none, STATS, health(false));
    expect(ids(g, 'hardware')).toEqual(['cpu', 'ram']);
    expect(ids(g, 'workload')).toEqual(['crv', 'typesense']);
    expect(g.notes.some((n) => /No NVIDIA GPU/.test(n))).toBe(true);
  });

  it('draws nothing before the rig has loaded', () => {
    expect(buildRigGraph(null, null, null)).toMatchObject({ nodes: [], edges: [], notes: [], editable: false });
  });

  // One row of seven cards was a thin strip in a tall canvas (2026-09-20). The
  // cards and what runs on them form one band, the host another beneath it.
  it('lays the machine out in two bands: the cards, then the host', () => {
    const g = buildRigGraph(labRig(PARKED), STATS, health(false));
    const y = (id: string) => g.nodes.find((n) => n.id === id)!.position.y;
    expect(y('core')).toBe(y('other-0'));
    expect(y('core')).toBe(y('embedder'));
    expect(y('gpu-0')).toBeGreaterThan(y('core'));
    expect(y('crv')).toBeGreaterThan(y('gpu-0'));
    expect(y('crv')).toBe(y('typesense'));
    expect(y('cpu')).toBeGreaterThan(y('crv'));
    expect(y('cpu')).toBe(y('ram'));
    // every link stays inside its band: nothing from the host row crosses the cards
    for (const e of g.edges) expect(y(e.target)).toBeGreaterThan(y(e.source));
    // the frame is the drawing's own shape: five cards wide, four rows tall
    expect(g.size.w).toBe(5 * HW_W + 4 * 24);
    expect(g.size.h).toBeGreaterThan(900);
  });

  it('never stacks two cards on top of each other, and centres each workload over what it runs on', () => {
    const g = buildRigGraph(labRig(PARKED), STATS, health(false));
    const rows = new Map<number, number[]>();
    for (const n of g.nodes) rows.set(n.position.y, [...(rows.get(n.position.y) ?? []), n.position.x]);
    for (const xs of rows.values()) {
      xs.sort((a, b) => a - b);
      for (let i = 1; i < xs.length; i++) expect(xs[i] - xs[i - 1]).toBeGreaterThanOrEqual(WL_W);
    }
    const x = (id: string) => g.nodes.find((n) => n.id === id)!.position.x;
    expect(x('core') + WL_W / 2).toBeCloseTo((x('gpu-1') + HW_W + x('gpu-2')) / 2, 0);
    // the host row is centred under the wider row of cards
    expect(x('cpu') + HW_W + 12).toBeCloseTo((x('gpu-0') + x('gpu-4') + HW_W) / 2, 0);
  });
});

describe('placement', () => {
  it('reads the saved placement out of /api/rig', () => {
    expect(placementOf(labRig([]).next)).toEqual({ gpus: [0, 1, 2, 3], embed: { device: 'cuda', gpus: [4] }, share: 0.92 });
    expect(placementOf({ ...labRig([]).next!, embed: { device: 'cpu' } }).embed).toEqual({ device: 'cpu', gpus: [] });
    expect(placementOf(null)).toEqual({ gpus: [], embed: { device: 'cpu', gpus: [] }, share: 0 });
  });
  it('compares placements by what they mean, not by order', () => {
    const a = { gpus: [3, 2], embed: { device: 'cpu' as const, gpus: [] }, share: 0.92 };
    expect(samePlacement(a, { gpus: [2, 3], embed: { device: 'cpu', gpus: [4] }, share: 0.92 })).toBe(true);
    expect(samePlacement(a, { gpus: [2], embed: { device: 'cpu', gpus: [] }, share: 0.92 })).toBe(false);
    // "allocate less": the same cards at a smaller share is a different placement
    expect(samePlacement(a, { ...a, share: 0.8 })).toBe(false);
  });
});

describe('saying things', () => {
  it('says a placement in one line, the same for a draft and a saved layout', () => {
    expect(describePlacement({ gpus: [2, 3], embed: { device: 'cuda', gpus: [4] }, share: 0.9 })).toBe('Core on GPU 2, 3 at 90% · embedder on GPU 4');
    expect(describePlacement({ gpus: [], embed: { device: 'cpu' } })).toBe('Core on no card · embedder on the CPU');
  });
  it('says the machine from what it reports, whoever made it', () => {
    expect(machineLine(labRig([]), STATS)).toBe('5 GPUs · 108 GB VRAM  ·  AMD Ryzen 9 7950X  ·  64 GB RAM');
    const intel: SysStats = { cpu: { name: 'Intel(R) Core(TM) i9-14900K', cores: 32, temp: 0, util: 0 }, ram: { used: 1, total: 65536 } };
    expect(machineLine({ inventory: { gpus: null, nvlink: false }, observed: null, next: null }, intel)).toBe('no GPU  ·  Intel(R) Core(TM) i9-14900K  ·  64 GB RAM');
    expect(machineLine(null, null)).toBe('no GPU');
  });
});

describe('unitLabel', () => {
  it('shortens a docker scope and strips unit suffixes', () => {
    expect(unitLabel('docker-0123456789abcdef0123456789abcdef0123.scope', 'python3')).toBe('docker 0123456789ab');
    expect(unitLabel('cerveau-embed.service', 'python3')).toBe('cerveau-embed');
    expect(unitLabel(undefined, 'python3')).toBe('python3');
  });
});
