// Idle store — when the Core is going to park, and how long is left.
//
// Polled rather than pushed: the countdown only needs second-ish resolution,
// and a poll keeps working across a core restart, which is exactly the moment
// a pushed stream would be dead. The interval tightens inside the warning
// window so the visible clock does not tick in 30-second jumps.
import { api, type IdleStatus } from '../api';

const CALM_MS = 30_000;
const WARN_MS = 5_000;

let status = $state<IdleStatus | null>(null);
let timer: ReturnType<typeof setTimeout> | null = null;

function nextDelay(): number {
  const s = status?.state;
  return s === 'warning' || s === 'waking' ? WARN_MS : CALM_MS;
}

function schedule(): void {
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => void tick(), nextDelay());
}

async function tick(): Promise<void> {
  try {
    status = await api.idleStatus();
  } catch {
    // A failed poll is not evidence of anything — leave the last known state
    // rather than flashing the screen away and back.
  }
  schedule();
}

export const idleStore = {
  get value(): IdleStatus | null { return status; },
  get state(): string { return status?.state ?? 'active'; },
  /** Only an upcoming park needs the interrupting warning card. */
  get visible(): boolean {
    const s = status?.state;
    return s === 'warning';
  },
  get notice(): string {
    if (status?.state === 'parked') return 'Core is already idle. Send a message to wake it.';
    if (status?.state === 'waking') return 'Core is waking. Your conversation is still here.';
    return '';
  },

  /** replace state after a user action, so the UI does not wait for a poll */
  set(next: IdleStatus | null): void {
    if (next) status = next;
    schedule();
  },

  start(): void {
    if (timer) return;
    void tick();
  },
  stop(): void {
    if (timer) { clearTimeout(timer); timer = null; }
  },
};
