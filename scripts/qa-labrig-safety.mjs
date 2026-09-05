import { writeFileSync } from 'node:fs';

// Claim before any browser command or screenshot. Later saves belong only to
// this attempt; a second invocation cannot overwrite its retained evidence.
export function claimEvidence(file, initial) {
  writeFileSync(file, JSON.stringify(initial, null, 2) + '\n', { flag: 'wx', mode: 0o600 });
}

export function fetchWithin(url, options = {}, timeoutMs = 10_000) {
  return fetch(url, { ...options, signal: AbortSignal.timeout(timeoutMs) });
}
