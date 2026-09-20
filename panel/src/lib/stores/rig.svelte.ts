// Rig store — the machine and where the active Core sits on it, polled only
// while the Rig section is open. /api/rig costs two nvidia-smi calls a time,
// so it runs slowly; the live meters (/api/system/stats) run faster.
import { api } from '../api';
import type { Rig, SysStats } from '../types';

const RIG_MS = 5000;
const STATS_MS = 2000;

let rig = $state<Rig | null>(null);
let stats = $state<SysStats | null>(null);
// 'unsupported': the core answered, but not on /api/rig — an older binary.
let phase = $state<'loading' | 'ready' | 'unsupported'>('loading');
let timers: ReturnType<typeof setInterval>[] = [];
// '' = whichever profile is live. Set, the diagram shows THAT profile: real
// time if it is the loaded one, a preview of where it would go if it is not.
let core = $state('');

async function loadRig(): Promise<void> {
  const asked = core;
  const r = await api.rig(asked || undefined);
  if (asked !== core) return;           // the user picked another profile meanwhile
  if (r) { rig = r; phase = 'ready'; }
  else if (!rig) phase = 'unsupported'; // keep the last good picture through a blip
}
async function loadStats(): Promise<void> {
  const s = await api.systemStats();
  if (s) stats = s;
}

export const rigStore = {
  get rig(): Rig | null { return rig; },
  get stats(): SysStats | null { return stats; },
  get phase() { return phase; },

  get core(): string { return core; },
  /** show another profile; the diagram redraws as soon as it answers */
  select(id: string): Promise<void> { core = id; return loadRig(); },

  /** right after a save: do not wait for the next poll to show it */
  refresh(): Promise<void> { return loadRig(); },

  start(): void {
    if (timers.length) return;
    void loadRig(); void loadStats();
    timers = [setInterval(() => void loadRig(), RIG_MS), setInterval(() => void loadStats(), STATS_MS)];
  },
  stop(): void {
    timers.forEach(clearInterval);
    timers = [];
  },
};
