// Health store — the core's live status, polled. Owns the offline verdict so
// every component reads one truth instead of interpreting `health == null`.
import { api } from '../api';
import type { Health } from '../types';

const POLL_MS = 5000;
/** consecutive failed polls before we call the core offline (not one blip) */
const OFFLINE_AFTER = 2;

let health = $state<Health | null>(null);
let misses = $state(0);
let timer: ReturnType<typeof setInterval> | null = null;

export const healthStore = {
  get value(): Health | null { return health; },
  get offline(): boolean { return misses >= OFFLINE_AFTER; },
  get workspace(): string { return health?.workspace ?? ''; },
  get version(): string { return health?.system?.version ?? ''; },

  async refresh(): Promise<void> {
    const h = await api.health();
    if (h) { health = h; misses = 0; }
    else misses += 1;
  },

  start(): void {
    if (timer) return;
    void this.refresh();
    timer = setInterval(() => void this.refresh(), POLL_MS);
  },
  stop(): void {
    if (timer) { clearInterval(timer); timer = null; }
  },
};
