#!/usr/bin/env node
// Explicit opt-in runner. Default mode prepares retained fixtures without an
// inference request. --run exercises the production API/loop in a test-only
// host; never launch cmd/crv, touch installed binaries or restart any service.
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawn, execFileSync } from 'node:child_process';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { assertRecoveryIdleWindow } from './qa-labrig-safety.mjs';

const root = resolve(fileURLToPath(new URL('..', import.meta.url)));
const args = process.argv.slice(2);
const cases = { baseline: 'TestRecoveryLiveRegionalBaseline', 'minecraft-copy': 'TestRecoveryLiveMinecraftCopy' };
const which = args.find(arg => Object.hasOwn(cases, arg)) || 'baseline';
if (args.some(arg => arg !== '--run' && !Object.hasOwn(cases, arg)) || args.filter(arg => Object.hasOwn(cases, arg)).length > 1) {
  throw Error('Usage: node scripts/qa-recovery-live.mjs [baseline|minecraft-copy] [--run]');
}
const live = args.includes('--run');
let idlePreflight;
mkdirSync(join(root, 'build'), { recursive: true });
const destination = mkdtempSync(join(root, 'build', 'recovery-live-'));
const cfg = JSON.parse(readFileSync('/home/shiny/.config/cerveau/config.json', 'utf8'));
const endpoint = process.env.CERVEAU_ACCEPTANCE_MODEL_URL || cfg.endpoints?.model;
if (live && (!process.env.CRV_MODEL_NAME || !process.env.CRV_MODEL_KEY)) {
  throw Error('Live acceptance requires CRV_MODEL_NAME and CRV_MODEL_KEY inherited privately. Do not echo credentials or change Core configuration.');
}
if (live) {
  // Refuse concurrent inference with any production session. Merely reading
  // /api/sessions is not a production run control or settings mutation.
  const response = await fetch('http://localhost:7700/api/sessions', { signal: AbortSignal.timeout(10_000) });
  if (!response.ok) throw Error('Cannot verify production is idle; refuse live acceptance.');
  const state = await response.json();
  if (!Array.isArray(state.running)) throw Error('Cannot identify running production sessions; refuse live acceptance.');
  if (state.running.length) throw Error('Production run is active; do not compete for its Core.');
  const idleResponse = await fetch('http://localhost:7700/api/idle', { signal: AbortSignal.timeout(10_000) });
  if (!idleResponse.ok) throw Error('Cannot observe production idle timer; refuse live acceptance.');
  idlePreflight = await idleResponse.json();
  assertRecoveryIdleWindow(idlePreflight);
}
const files = execFileSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'], { cwd: root, encoding: 'utf8' }).split('\0').filter(Boolean).sort();
const hash = createHash('sha256');
for (const file of [...new Set(files)]) { hash.update(file + '\0'); hash.update(readFileSync(join(root, file))); hash.update('\0'); }
const sourceSHA256 = hash.digest('hex');
const revision = `recovery-live-${sourceSHA256.slice(0, 12)}`;
const command = ['test', '-race', '-ldflags', `-X cerveau/internal/api.BuildRevision=${revision}`, './internal/api', '-run', `^${cases[which]}$`, '-count=1', '-v', '-timeout', '12m'];
writeFileSync(join(destination, 'launch.json'), JSON.stringify({
  mode: live ? 'live' : 'prepare-only', case: which, source_sha256: sourceSHA256,
  revision, command: ['go', ...command], started: new Date().toISOString(),
  source_scope: 'tracked and untracked non-ignored source files; test binary, not installable release',
  credential_handling: 'inherited CRV_MODEL_NAME/CRV_MODEL_KEY; key not retained',
  idle_preflight: idlePreflight,
  limits: { seconds_per_case: 600, model_calls: 32, completion_tokens: 70000 },
}, null, 2) + '\n', { flag: 'wx', mode: 0o600 });
console.log(`${live ? 'LIVE' : 'PREPARE ONLY'} isolated ${which}: ${destination}`);
const child = spawn('go', command, { cwd: root, stdio: 'inherit', env: {
  ...process.env,
  CERVEAU_RECOVERY_LIVE_ROOT: destination,
  CERVEAU_RECOVERY_LIVE_PREPARE: '1',
  CERVEAU_RECOVERY_LIVE_RUN: live ? '1' : '',
  CERVEAU_RECOVERY_MODEL_CTX: String(cfg.model_ctx || 32768),
  CERVEAU_ACCEPTANCE_MODEL_URL: endpoint || '',
} });
const exit = await new Promise((resolveExit, reject) => {
  child.once('error', reject);
  child.once('exit', (code, signal) => resolveExit({ code, signal }));
});
writeFileSync(join(destination, 'exit.json'), JSON.stringify({ ...exit, finished: new Date().toISOString() }, null, 2) + '\n', { flag: 'wx', mode: 0o600 });
process.exitCode = exit.code ?? 1;
