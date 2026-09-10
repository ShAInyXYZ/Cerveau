#!/usr/bin/env node
import { BrowserNativeError, MAX_INPUT, SCHEMA, runBrowserNative } from './engine.mjs';

// One bounded request. The host injects workspace; tool arguments cannot choose
// a runtime, executable, profile, output directory, or executable program.
let hardDeadline;
try {
  if (process.argv.length !== 3) throw new BrowserNativeError('INVALID_INPUT', 'usage: node runner.mjs browser_run|runtime_profile');
  hardDeadline = setTimeout(() => { process.stdout.write(JSON.stringify({ schema: SCHEMA, action: process.argv[2], ok: false, verdict: 'unverified', error: { code: 'RUNNER_DEADLINE', message: 'runner exceeded 29 second cleanup deadline' } }) + '\n'); process.exit(1); }, 29000);
  const chunks = []; let bytes = 0;
  for await (const chunk of process.stdin) { bytes += chunk.length; if (bytes > MAX_INPUT + 8192) throw new BrowserNativeError('INVALID_INPUT', 'request exceeds input byte cap'); chunks.push(chunk); }
  let parsed;
  try { parsed = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { throw new BrowserNativeError('INVALID_INPUT', 'stdin must be one JSON object'); }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed) || Object.keys(parsed).some(key => !['workspace', 'input'].includes(key))) throw new BrowserNativeError('INVALID_INPUT', 'request must contain only workspace and input');
  const result = await runBrowserNative(process.argv[2], parsed.input, { workspace: parsed.workspace });
  clearTimeout(hardDeadline); process.stdout.write(JSON.stringify(result) + '\n'); process.exitCode = result.ok ? 0 : 1;
} catch (error) {
  clearTimeout(hardDeadline); process.stdout.write(JSON.stringify({ schema: SCHEMA, action: process.argv[2], ok: false, verdict: 'unverified', error: { code: error.code ?? 'RUNNER_FAILED', message: String(error.message).slice(0, 500) } }) + '\n'); process.exitCode = 1;
}
