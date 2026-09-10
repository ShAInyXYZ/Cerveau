import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { assertRecoveryIdleWindow, claimEvidence, fetchWithin } from './qa-labrig-safety.mjs';

test('evidence ownership refuses reuse and preserves the first record', () => {
  const root = mkdtempSync(join(tmpdir(), 'labrig-evidence-guard-'));
  const file = join(root, 'browser-evidence.json');
  claimEvidence(file, { attempt: 1 });
  assert.throws(() => claimEvidence(file, { attempt: 2 }), { code: 'EEXIST' });
  assert.deepEqual(JSON.parse(readFileSync(file, 'utf8')), { attempt: 1 });
});

test('a stalled local request is bounded', async () => {
  const server = createServer(() => {});
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    await assert.rejects(fetchWithin(`http://127.0.0.1:${server.address().port}`, {}, 30), { name: 'TimeoutError' });
  } finally {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
  }
});

test('recovery requires actual idle runway, not an unrelated activity hold', () => {
  for (const seconds of [-1, 0, 600, 719, 720, undefined, NaN]) {
    assert.throws(() => assertRecoveryIdleWindow({ enabled: true, held: false, park_in_seconds: seconds }), /idle timer/);
  }
  assert.doesNotThrow(() => assertRecoveryIdleWindow({ enabled: true, held: false, park_in_seconds: 721 }));
  assert.throws(() => assertRecoveryIdleWindow({ enabled: true, held: true, park_in_seconds: -1 }), /idle timer/);
  assert.doesNotThrow(() => assertRecoveryIdleWindow({ enabled: false }));
  assert.throws(() => assertRecoveryIdleWindow({}), /Cannot verify/);
  assert.throws(() => assertRecoveryIdleWindow(null), /Cannot verify/);
});
