import { writeFileSync } from 'node:fs';

// Claim before any browser command or screenshot. Later saves belong only to
// this attempt; a second invocation cannot overwrite its retained evidence.
export function claimEvidence(file, initial) {
  writeFileSync(file, JSON.stringify(initial, null, 2) + '\n', { flag: 'wx', mode: 0o600 });
}

export function fetchWithin(url, options = {}, timeoutMs = 10_000) {
  return fetch(url, { ...options, signal: AbortSignal.timeout(timeoutMs) });
}

// Isolated inference does not hold the production host's idle timer. Observe
// it without changing settings or snoozing; leave enough time for the bounded
// case plus startup/cleanup. A missing/overdue countdown fails closed.
export function assertRecoveryIdleWindow(status, requiredSeconds = 720) {
  if (!status || typeof status.enabled !== 'boolean') throw Error('Cannot verify production idle policy; refuse isolated inference.');
  // A hold belongs to unrelated production work and may release immediately;
  // it is not a lease for this fixture. Require an actual positive runway.
  if (status.enabled && (!Number.isFinite(status.park_in_seconds) || status.park_in_seconds <= requiredSeconds)) {
    throw Error(`Production idle timer may park the Core within ${requiredSeconds}s; refuse isolated inference. Operator action required; runner will not snooze or change Core policy.`);
  }
}
