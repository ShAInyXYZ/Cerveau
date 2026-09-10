#!/usr/bin/env node
import { DevCheckError, runDevCheck } from './engine.mjs';

// Exactly one bounded JSON request. Diagnostics remain structured on stdout;
// the host uses the nonzero exit code as failure, never a misleading green card.
try {
  if (process.argv.length !== 3) throw new DevCheckError('INVALID_INPUT', 'usage: rfx-devcheck inspect|check|capture');
  const chunks = []; let bytes = 0;
  for await (const chunk of process.stdin) {
    bytes += chunk.length;
    if (bytes > 32 * 1024) throw new DevCheckError('INVALID_INPUT', 'input exceeds 32 KiB');
    chunks.push(chunk);
  }
  let parsed;
  try { parsed = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { throw new DevCheckError('INVALID_INPUT', 'stdin must be one JSON object'); }
  const result = await runDevCheck(process.argv[2], parsed);
  process.stdout.write(JSON.stringify(result) + '\n');
  process.exitCode = result.ok ? 0 : 1;
} catch (error) {
  process.stdout.write(JSON.stringify({ schema: 'cerveau.devcheck.v1', ok: false, verdict: 'unverified', error: { code: error.code ?? 'RUNNER_FAILED', message: error.message } }) + '\n');
  process.exitCode = 1;
}
