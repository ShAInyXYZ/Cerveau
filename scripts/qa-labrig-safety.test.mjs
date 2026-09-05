import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { claimEvidence, fetchWithin } from './qa-labrig-safety.mjs';

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
