import assert from 'node:assert/strict';
import test from 'node:test';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { mkdtemp, readFile, readdir, rm, symlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { SCHEMA, LIMITS, validateInput, requestAllowed, runBrowserNative, summarizeCPU, compactResult } from './engine.mjs';

const base = { url: 'http://localhost:4200', steps: [{ type: 'capture' }] };
const browserOptions = { skip: process.env.CERVEAU_PLAYWRIGHT_MODULE ? false : 'UNVERIFIED: existing Playwright/Chromium runtime not configured' };
const ownedWorkspaces = [];
const workspace = async () => { const path = await mkdtemp(join(tmpdir(), 'cerveau-browsernative-')); ownedWorkspaces.push(path); return path; };
test.after(async () => { await Promise.all(ownedWorkspaces.map(path => rm(path, { recursive: true, force: true }))); });
const receipt = async (root, result) => JSON.parse(await readFile(join(root, result.evidence.path), 'utf8'));

test('strict actions, local URL and unknown field validation', () => {
  for (const url of ['http://127.1:4200', 'http://2130706433:4200', 'http://127.0.0.1', 'http://localhost:80', 'https://localhost:4200',
    'http://[::1]:4200', 'http://localhost:4200/#x', 'http://user:pass@localhost:4200', 'http://127.0.0.1.evil:4200', 'file:///etc/passwd'])
    assert.throws(() => validateInput('browser_run', { ...base, url }), /URL_SCOPE/);
  assert.equal(validateInput('browser_run', base).max_bytes, 128 * 1024);
  for (const addition of [{ script: 'alert(1)' }, { workspace: '/' }, { executablePath: '/bin/sh' }, { url: 'http://localhost:4200', cdp_url: 'x' }])
    assert.throws(() => validateInput('browser_run', { ...base, ...addition }), /INVALID_INPUT/);
  assert.throws(() => validateInput('evaluate', base), /INVALID_INPUT/);
  assert.throws(() => validateInput('browser_run', { ...base, steps: [] }), /INVALID_INPUT/);
  assert.throws(() => validateInput('browser_run', { ...base, steps: Array(21).fill({ type: 'key', key: 'a' }) }), /INVALID_INPUT/);
});

test('typed selectors, conditions, keys, pointer coordinates, and capture budgets are bounded', () => {
  const rejected = [
    { type: 'evaluate', script: '1+1' }, { type: 'click', selector: 'xpath=//*' }, { type: 'click', selector: '#a >> text=x' },
    { type: 'click', selector: '#a', path: '/tmp/x' }, { type: 'fill', selector: '#a', value: 'x'.repeat(2001) },
    { type: 'key', key: 'Control+R' }, { type: 'key', key: 'a', hold_ms: 1001 }, { type: 'key', key: 'Enter', action: 'release', hold_ms: 1 },
    { type: 'pointer', action: 'move', x: 1280, y: 5 }, { type: 'pointer', action: 'drag', x: 3, y: 4 },
    { type: 'pointer', action: 'move', x: 0, y: 0, from_x: 1 }, { type: 'pointer', action: 'drag', x: 3, y: 4, from_x: 0, from_y: 0, duration_ms: 1001 },
    { type: 'check', condition: { selector: '#a', kind: 'text', expected: 3 } },
    { type: 'wait', condition: { selector: '#a', kind: 'visible', expected: true }, timeout_ms: 5001 },
    { type: 'capture', capture: 'before' }, { type: 'capture', id: '../escape' },
  ];
  for (const step of rejected) assert.throws(() => validateInput('browser_run', { ...base, steps: [step] }), /INVALID_INPUT/, JSON.stringify(step));
  assert.throws(() => validateInput('browser_run', { ...base, steps: [{ type: 'capture', id: 'same' }, { type: 'capture', id: 'same' }] }), /INVALID_INPUT/);
  assert.throws(() => validateInput('browser_run', { ...base, steps: Array(7).fill({ type: 'capture' }) }), /INVALID_INPUT/);
  assert.throws(() => validateInput('browser_run', { ...base, steps: Array(5).fill({ type: 'click', selector: '#a', capture: 'both' }) }), /INVALID_INPUT/);
  assert.equal(validateInput('browser_run', { ...base, steps: [{ type: 'key', key: 'ArrowRight', action: 'hold', hold_ms: 1000 }] }).steps[0].hold_ms, 1000);
  assert.equal(validateInput('runtime_profile', { url: base.url }).duration_ms, 2000);
  for (const addition of [{ duration_ms: 999 }, { duration_ms: 5001 }, { sampling_interval_us: 100 }, { max_hotspots: 21 }, { trace_path: '/tmp/x' }])
    assert.throws(() => validateInput('runtime_profile', { url: base.url, ...addition }), /INVALID_INPUT/);
});

test('read-only exact-origin request fence', () => {
  for (const [url, method] of [[base.url, 'POST'], [base.url, 'PUT'], ['http://127.0.0.1:4200', 'GET'], ['http://localhost:4201', 'GET'],
    ['http://u:p@localhost:4200', 'GET'], ['https://localhost:4200', 'GET'], ['file:///etc/passwd', 'GET'], ['ws://localhost:4200', 'GET']])
    assert.equal(requestAllowed(url, method, base.url), false);
  assert.equal(requestAllowed(base.url + '/app.js', 'GET', base.url), true);
  assert.equal(requestAllowed(base.url + '/health', 'HEAD', base.url), true);
});

test('CPU summarization is capped, source-located, and never fabricates missing metrics', () => {
  assert.equal(summarizeCPU(null).available, false);
  const profile = { startTime: 0, endTime: 3000, nodes: [{ id: 1, callFrame: { functionName: 'burn', url: base.url + '/app.js?secret=yes', lineNumber: 9, columnNumber: 4 } }], samples: [1, 1], timeDeltas: [1000, 2000] };
  const cpu = summarizeCPU(profile);
  assert.equal(cpu.available, true); assert.equal(cpu.hotspots[0].function, 'burn'); assert.equal(cpu.hotspots[0].line, 10);
  assert.equal(cpu.hotspots[0].self_ms, 3); assert.ok(!JSON.stringify(cpu).includes('secret=yes')); assert.equal(cpu.raw_profile_retained, false);
  const tooBig = summarizeCPU({ ...profile, nodes: Array(LIMITS.cpuNodes + 1).fill(profile.nodes[0]) });
  assert.equal(tooBig.available, false); assert.equal(tooBig.truncated, true);
  assert.equal(summarizeCPU({ ...profile, samples: Array(LIMITS.cpuSamples + 1).fill(1) }).available, false);
});

test('summary retains valid bounded JSON and all statuses/artifact identities', () => {
  const text = '\u0001'.repeat(500); const artifact = { path: '.devcheck/2026-01-01T00-00-00-000Z-12345678-1234-1234-1234-123456789abc/step-1-before.jpg', bytes: 1000, sha256: 'a'.repeat(64), mime: 'image/jpeg', role: 'context-image', width: 1280, height: 960 };
  const result = compactResult({ schema: SCHEMA, ok: false, verdict: 'failed', error: { code: 'CHECK_FAILED', message: text }, artifacts: Array(8).fill(artifact),
    evidence: { ...artifact, path: artifact.path.replace('step-1-before.jpg', 'evidence.json') },
    steps: Array.from({ length: 20 }, (_, index) => ({ id: `step-${index}`, type: 'check', status: 'passed', elapsed_ms: 4, actual: text, condition: { expected: text }, evidence: [artifact.path] })) });
  assert.ok(Buffer.byteLength(JSON.stringify(result)) <= 7000); assert.equal(result.steps.length, 20); assert.equal(result.artifacts.length, 8);
  assert.equal(result.artifacts[0].sha256, artifact.sha256);
});

test('symlink evidence directory cannot write outside workspace', async () => {
  const root = await workspace(); const outside = await workspace(); await symlink(outside, join(root, '.devcheck'));
  await assert.rejects(() => runBrowserNative('browser_run', base, { workspace: root }), /ARTIFACT_SCOPE/);
  assert.deepEqual(await readdir(outside), []);
});

async function invokeCLI(input, env = {}) {
  const child = spawn(process.execPath, [new URL('./runner.mjs', import.meta.url).pathname, 'browser_run'], { env: { ...process.env, ...env }, stdio: ['pipe', 'pipe', 'pipe'] });
  let stdout = ''; let stderr = ''; child.stdout.on('data', data => { stdout += data; }); child.stderr.on('data', data => { stderr += data; });
  child.stdin.on('error', () => {}); child.stdin.end(typeof input === 'string' ? input : JSON.stringify(input));
  const [code] = await once(child, 'exit'); return { code, stdout, stderr, result: JSON.parse(stdout) };
}
test('runner rejects malformed, oversized, and executable inputs with one JSON failure', async () => {
  const root = await workspace();
  for (const input of ['not json', 'x'.repeat(42 * 1024), { workspace: root, input: { ...base, script: 'alert(1)' } }, { workspace: root, input: base, runtime: '/bin/sh' }]) {
    const result = await invokeCLI(input); assert.equal(result.code, 1); assert.equal(result.stderr, ''); assert.equal(result.result.ok, false); assert.equal(result.result.error.code, 'INVALID_INPUT');
  }
});
test('missing installed dependency retains an unverified receipt', async () => {
  const root = await workspace(); const response = await invokeCLI({ workspace: root, input: base }, { CERVEAU_PLAYWRIGHT_MODULE: join(root, 'missing.mjs') });
  assert.equal(response.code, 1); assert.equal(response.result.error.code, 'DEPENDENCY_MISSING');
  assert.equal((await receipt(root, response.result)).verdict, 'unverified');
});

async function fixture() {
  const calls = [];
  const outsider = createServer((req, res) => { calls.push(`ESCAPE ${req.url}`); res.end('outside'); });
  outsider.listen(0, '127.0.0.1'); await once(outsider, 'listening');
  const server = createServer((req, res) => {
    calls.push(`${req.method} ${req.url}`);
    if (req.url === '/redirect') { res.writeHead(302, { location: `http://127.0.0.1:${outsider.address().port}/escape` }); res.end(); return; }
    if (req.url.startsWith('/resource')) { res.end('resource'); return; }
    if (req.url === '/large') { res.setHeader('Content-Type', 'text/html'); res.end('x'.repeat(3 * 1024 * 1024)); return; }
    if (req.url === '/busy.js') { res.setHeader('Content-Type', 'text/javascript'); res.end('function fixtureBurn(){let n=0;const until=performance.now()+90;while(performance.now()<until){n+=Math.sqrt(n+1)}return n}setInterval(fixtureBurn,110);fixtureBurn();'); return; }
    if (req.url === '/blocked.js') { res.setHeader('Content-Type', 'text/javascript'); res.end('function permanentlyBlockedFixture(){let n=0;while(true){n=Math.sqrt(n+1)}}permanentlyBlockedFixture();'); return; }
    res.setHeader('Content-Type', 'text/html');
    if (req.url === '/overflow') { res.end('<!doctype html><h1>Resource overflow fixture</h1><script>for(let n=0;n<85;n++)fetch("/resource?n="+n)</script>'); return; }
    if (req.url === '/busy' || req.url === '/blocked') { res.end(`<!doctype html><h1>Profile fixture</h1><script src="${req.url === '/busy' ? '/busy.js' : '/blocked.js'}"></script>`); return; }
    res.end(`<!doctype html><meta charset="utf-8"><title>Interaction fixture</title>
      <button id="click">Click</button><input id="input"><output id="click-state">0</output><output id="input-state">empty</output>
      <output id="key-state">none</output><output id="drag-state">none</output><div id="delayed">loading</div>
      <div class="duplicate">one</div><div class="duplicate">two</div><div id="hidden" hidden>hidden</div>
      <canvas id="canvas" width="300" height="150" style="position:absolute;left:100px;top:200px;background:#ace"></canvas>
      <script>
        let clicks=0,down=false;
        document.querySelector('#click').onclick=e=>document.querySelector('#click-state').textContent=String(++clicks)+':'+e.isTrusted;
        document.querySelector('#input').oninput=e=>document.querySelector('#input-state').textContent=e.target.value;
        document.addEventListener('keyup',e=>document.querySelector('#key-state').textContent=e.key+':'+e.isTrusted);
        const canvas=document.querySelector('#canvas');
        const pixels=new ImageData(300,150);let seed=19;for(let n=0;n<pixels.data.length;n++){seed=(seed*1664525+1013904223)>>>0;pixels.data[n]=n%4===3?255:seed>>>24}canvas.getContext('2d').putImageData(pixels,0,0);
        canvas.addEventListener('pointerdown',e=>{down=e.isTrusted});canvas.addEventListener('pointerup',e=>{document.querySelector('#drag-state').textContent=down&&e.isTrusted?'trusted drag':'untrusted';down=false});
        setTimeout(()=>document.querySelector('#delayed').textContent='ready',300);
        fetch('/resource').catch(()=>{}); fetch('/mutation',{method:'POST',body:'blocked'}).catch(()=>{});
        fetch('http://127.0.0.1:${outsider.address().port}/escape').catch(()=>{});
        new WebSocket('ws://127.0.0.1:${server.address().port}/socket');
      </script>`);
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  return { url: `http://127.0.0.1:${server.address().port}`, calls, close: async () => {
    server.closeAllConnections(); outsider.closeAllConnections();
    await Promise.all([new Promise(resolve => server.close(resolve)), new Promise(resolve => outsider.close(resolve))]);
  } };
}

test('real browser executes same-page trusted click/key/drag, fill, conditions, and capped captures', browserOptions, async () => {
  const app = await fixture(); const root = await workspace();
  try {
    const result = await runBrowserNative('browser_run', { url: app.url, wait_ms: 0, max_bytes: 16 * 1024, steps: [
      { id: 'click', type: 'click', selector: '#click', capture: 'both' },
      { type: 'check', condition: { selector: '#click-state', kind: 'text', expected: '1:true' } },
      { type: 'fill', selector: '#input', value: 'filled text' },
      { type: 'check', condition: { selector: '#input-state', kind: 'text', expected: 'filled text' } },
      { type: 'key', key: 'ArrowRight', action: 'hold', hold_ms: 30 },
      { type: 'check', condition: { selector: '#key-state', kind: 'text', expected: 'ArrowRight:true' } },
      { type: 'pointer', action: 'move', x: 125, y: 225, duration_ms: 30 },
      { type: 'pointer', action: 'drag', from_x: 125, from_y: 225, x: 250, y: 280, duration_ms: 100 },
      { type: 'check', condition: { selector: '#drag-state', kind: 'text', expected: 'trusted drag' } },
      { type: 'wait', condition: { selector: '#delayed', kind: 'text', expected: 'ready' }, timeout_ms: 1500 },
      { type: 'check', condition: { selector: '.duplicate', kind: 'count', expected: 2 } },
      { type: 'check', condition: { selector: '#hidden', kind: 'visible', expected: false } },
      { type: 'capture' },
    ] }, { workspace: root });
    assert.equal(result.ok, true, JSON.stringify(result)); assert.equal(result.verdict, 'passed'); assert.equal(result.steps.length, 13);
    assert.equal(result.artifacts.length, 3);
    for (const image of result.artifacts) { assert.ok(image.bytes <= 16 * 1024); assert.equal(image.mime, 'image/jpeg'); assert.match(image.sha256, /^[a-f0-9]{64}$/); }
    assert.ok(result.artifacts.some(image => 'quality' in image), 'noisy viewport must exercise bounded re-encoding');
    const evidence = await receipt(root, result); assert.ok(evidence.blocked_requests.some(item => item.method === 'POST'));
    assert.ok(evidence.blocked_requests.some(item => item.kind === 'websocket'));
    assert.ok(!app.calls.some(item => item.startsWith('ESCAPE') || item.startsWith('POST')));
    const fresh = await runBrowserNative('browser_run', { url: app.url, steps: [{ type: 'check', condition: { selector: '#click-state', kind: 'text', expected: '0' } }] }, { workspace: root });
    assert.equal(fresh.ok, true); assert.notEqual(fresh.evidence.path, result.evidence.path);
  } finally { await app.close(); }
});

test('real browser stops on false checks and ambiguity; readiness timeout stays unverified', browserOptions, async () => {
  const app = await fixture(); const root = await workspace();
  try {
    for (const [step, verdict, code] of [
      [{ type: 'check', condition: { selector: '#click-state', kind: 'text', expected: 'wrong' } }, 'failed', 'CHECK_FAILED'],
      [{ type: 'click', selector: '.duplicate' }, 'failed', 'AMBIGUOUS_TARGET'],
      [{ type: 'wait', condition: { selector: '#absent', kind: 'exists', expected: true }, timeout_ms: 100 }, 'unverified', 'WAIT_TIMEOUT'],
    ]) {
      const result = await runBrowserNative('browser_run', { url: app.url, steps: [step, { type: 'capture' }] }, { workspace: root });
      assert.equal(result.ok, false); assert.equal(result.verdict, verdict); assert.equal(result.error.code, code); assert.equal(result.steps.length, 1); assert.equal(result.artifacts.length, 0);
    }
    const executed = await runBrowserNative('browser_run', { url: app.url, steps: [{ type: 'click', selector: '#click' }] }, { workspace: root });
    assert.equal(executed.verdict, 'executed', 'input delivery alone cannot prove application correctness');
    const escaped = await runBrowserNative('browser_run', { url: app.url + '/redirect', steps: [{ type: 'capture' }] }, { workspace: root });
    assert.equal(escaped.verdict, 'unverified'); assert.ok(!app.calls.some(item => item.startsWith('ESCAPE')));
  } finally { await app.close(); }
});

test('real CPU profiling identifies source hotspots and reports observational metrics', browserOptions, async () => {
  const app = await fixture(); const root = await workspace();
  try {
    const result = await runBrowserNative('runtime_profile', { url: app.url + '/busy', duration_ms: 1200 }, { workspace: root });
    assert.equal(result.ok, true, JSON.stringify(result)); assert.equal(result.verdict, 'profiled');
    assert.equal(result.profile.cpu.available, true); assert.ok(result.profile.cpu.samples > 0);
    assert.ok(result.profile.cpu.hotspots.some(item => item.function === 'fixtureBurn' && item.url.endsWith('/busy.js')));
    assert.equal(result.profile.long_tasks.available, true); assert.ok(result.profile.long_tasks.count > 0);
    assert.equal(result.profile.frames.available, true); assert.equal(result.profile.cpu.raw_profile_retained, false);
    assert.match(result.profile.application_correctness, /does not prove/);
    assert.ok(result.evidence.bytes <= LIMITS.receiptBytes);
  } finally { await app.close(); }
});

test('real permanently blocked page still yields CPU evidence; frames are unavailable, not zero', browserOptions, async () => {
  const app = await fixture(); const root = await workspace(); const started = Date.now();
  try {
    const result = await runBrowserNative('runtime_profile', { url: app.url + '/blocked', duration_ms: 1000 }, { workspace: root });
    assert.equal(result.verdict, 'profiled', JSON.stringify(result));
    assert.equal(result.profile.navigation.committed, true); assert.equal(result.profile.navigation.dom_ready, false);
    assert.ok(result.profile.cpu.hotspots.some(item => item.function === 'permanentlyBlockedFixture' && item.url.endsWith('/blocked.js')));
    assert.equal(result.profile.frames.available, false); assert.equal(result.profile.frames.count, undefined);
    assert.equal(result.profile.long_tasks.available, false); assert.equal(result.profile.long_tasks.count, undefined);
    assert.equal(result.profile.renderer_paused_at_end, true);
    assert.ok(Date.now() - started < 12000, 'host-side sampling and cleanup must not await blocked renderer JS');
    const evidence = await receipt(root, result); assert.equal(evidence.profile.navigation.dom_ready, false);
  } finally { await app.close(); }
});

test('real observer overflow admits omitted timing entries and oversized responses fail closed', browserOptions, async () => {
  const app = await fixture(); const root = await workspace();
  try {
    const result = await runBrowserNative('runtime_profile', { url: app.url + '/overflow', duration_ms: 1200 }, { workspace: root });
    assert.equal(result.verdict, 'profiled', JSON.stringify(result));
    const evidence = await receipt(root, result);
    assert.equal(evidence.profile.resources.entries.length, LIMITS.events);
    assert.equal(evidence.profile.truncated.resources, true);
    const tooLarge = await runBrowserNative('browser_run', { url: app.url + '/large', steps: [{ type: 'capture' }] }, { workspace: root });
    assert.equal(tooLarge.verdict, 'unverified');
    assert.ok((await receipt(root, tooLarge)).blocked_requests.some(item => item.reason === 'response byte budget exceeded'));
  } finally { await app.close(); }
});
