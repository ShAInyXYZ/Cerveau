import assert from 'node:assert/strict';
import { mkdtemp, readFile, symlink, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import test from 'node:test';
import { validateInput, requestAllowed, runDevCheck, compactResult } from './engine.mjs';

const runtime = new URL('./rfx-devcheck.mjs', import.meta.url);
const browserAvailable = Boolean(process.env.CERVEAU_PLAYWRIGHT_MODULE);

test('accept only an explicit loopback HTTP app origin and known parameters', () => {
  for (const url of ['https://example.org:4444', 'http://127.0.0.1', 'file:///etc/passwd',
    'http://127.1:4200', 'http://2130706433:4200', 'http://127.0.0.1.evil:4200',
    'http://user:password@localhost:4200', 'http://localhost:80', 'http://[::1]:4200',
    'https://localhost:4200', 'http://localhost:4200/#secret']) {
    assert.throws(() => validateInput('inspect', { url }), /URL_SCOPE/);
  }
  assert.equal(validateInput('inspect', { url: 'http://localhost:4200/app?q=1' }).url, 'http://localhost:4200/app?q=1');
  assert.throws(() => validateInput('inspect', { url: 'http://localhost:4200', script: 'alert(1)' }), /INVALID_INPUT/);
  assert.throws(() => validateInput('inspect', { url: 'http://localhost:4200', workspace: '/' }), /INVALID_INPUT/);
  assert.throws(() => validateInput('inspect', { url: 'http://localhost:4200', wait_ms: 90000 }), /INVALID_INPUT/);
  assert.throws(() => validateInput('shell', { url: 'http://localhost:4200' }), /INVALID_INPUT/);
});

test('network policy fences method, origin, port, credentials, and protocols', () => {
  const origin = 'http://localhost:4200';
  assert.equal(requestAllowed(origin + '/assets/app.js', 'GET', origin), true);
  assert.equal(requestAllowed(origin + '/health', 'HEAD', origin), true);
  for (const [url, method] of [[origin, 'POST'], [origin, 'DELETE'], ['http://localhost:4201', 'GET'],
    ['http://127.0.0.1:4200', 'GET'], ['http://192.168.1.1:4200', 'GET'],
    ['https://localhost:4200', 'GET'], ['file:///etc/passwd', 'GET'],
    ['http://a:b@localhost:4200', 'GET'], ['ws://localhost:4200', 'GET']]) {
    assert.equal(requestAllowed(url, method, origin), false, `${method} ${url}`);
  }
});

test('structured stdout remains complete JSON below ingress cap even with adversarial diagnostic text', () => {
  const large = '\u0001'.repeat(500);
  const artifact = { path: '.devcheck/receipt/capture.jpg', sha256: 'a'.repeat(64), bytes: 1200, mime: 'image/jpeg', role: 'context-image', width: 200, height: 120 };
  const result = compactResult({ schema: 'cerveau.devcheck.v1', ok: false, artifacts: [artifact], error: { code: 'BROWSER_FAILED', message: large },
    facts: { page: { title: large, url: large }, labels: Array.from({ length: 8 }, () => ({ name: large })), errors: [large, large] },
    failed_checks: Array.from({ length: 4 }, () => ({ selector: large, expected: large, actual: large })) });
  assert.ok(Buffer.byteLength(JSON.stringify(result)) <= 5500);
  assert.equal(result.summary_truncated, true);
  assert.deepEqual(result.artifacts, [artifact]);
});

test('checks and screenshot arguments are bounded and cannot carry scripts or paths', () => {
  const base = { url: 'http://localhost:4200' };
  for (const checks of [[], [{ selector: '#app', kind: 'evaluate', expected: true }],
    [{ selector: '#app', kind: 'visible', expected: 'yes' }], [{ selector: '#app', kind: 'text', expected: 1 }],
    Array.from({ length: 21 }, () => ({ selector: 'body', kind: 'exists', expected: true }))]) {
    assert.throws(() => validateInput('check', { ...base, checks }), /INVALID_INPUT/);
  }
  assert.throws(() => validateInput('capture', { ...base, target: 'selector' }), /INVALID_INPUT/);
  assert.throws(() => validateInput('capture', { ...base, target: 'rectangle', rectangle: { x: -1, y: 0, width: 100, height: 100 } }), /INVALID_INPUT/);
  assert.throws(() => validateInput('capture', { ...base, target: 'page', path: '../../capture.jpg' }), /INVALID_INPUT/);
  assert.throws(() => validateInput('capture', { ...base, target: 'page', selector: '#extra' }), /INVALID_INPUT/);
  assert.throws(() => validateInput('capture', { ...base, target: 'page', max_bytes: 10 }), /INVALID_INPUT/);
});

test('reject symlinked evidence root without writing outside the workspace', async () => {
  const workspace = await mkdtemp(join(tmpdir(), 'devcheck-symlink-'));
  const outside = await mkdtemp(join(tmpdir(), 'devcheck-outside-'));
  await symlink(outside, join(workspace, '.devcheck'));
  await assert.rejects(() => runDevCheck('inspect', { url: 'http://localhost:4200' }, { workspace }), /ARTIFACT_SCOPE/);
  assert.deepEqual(await readdir(outside), []);
});

test('CLI uses structured errors for invalid/oversized JSON without launching a browser', async () => {
  for (const input of ['not JSON', JSON.stringify({ url: 'https://example.org:8443' }), 'x'.repeat(33 * 1024)]) {
    const child = spawn(process.execPath, [runtime.pathname, 'inspect'], { stdio: ['pipe', 'pipe', 'pipe'] });
    let stdout = ''; let stderr = '';
    child.stdout.on('data', data => { stdout += data; });
    child.stderr.on('data', data => { stderr += data; });
    child.stdin.end(input);
    const [exitCode] = await once(child, 'exit');
    assert.equal(exitCode, 1);
    const result = JSON.parse(stdout);
    assert.equal(result.ok, false);
    assert.match(result.error.code, /INVALID_INPUT|URL_SCOPE/);
    assert.equal(stderr, '');
  }
});

test('missing runtime dependency is a retained unverified receipt, not a successful inspection', async () => {
  const workspace = await mkdtemp(join(tmpdir(), 'devcheck-dependency-'));
  const child = spawn(process.execPath, [runtime.pathname, 'inspect'], {
    cwd: workspace, env: { ...process.env, CERVEAU_PLAYWRIGHT_MODULE: join(workspace, 'not-installed.mjs') },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stdout = ''; child.stdout.on('data', data => { stdout += data; });
  child.stdin.end(JSON.stringify({ url: 'http://localhost:4200' }));
  const [exitCode] = await once(child, 'exit');
  assert.equal(exitCode, 1);
  const result = JSON.parse(stdout);
  assert.equal(result.error.code, 'DEPENDENCY_MISSING');
  assert.equal(result.verdict, 'unverified');
  assert.equal(JSON.parse(await readFile(join(workspace, result.evidence.path), 'utf8')).verdict, 'unverified');
});

async function fixture() {
  const calls = [];
  const outsider = createServer((request, response) => { calls.push(`ESCAPE ${request.url}`); response.end('outside'); });
  outsider.listen(0, '127.0.0.1'); await once(outsider, 'listening');
  const server = createServer((request, response) => {
    calls.push(`${request.method} ${request.url}`);
    if (request.url === '/redirect') { response.writeHead(302, { location: `http://127.0.0.1:${outsider.address().port}/escaped` }); response.end(); return; }
    if (request.url === '/missing') { response.writeHead(503); response.end('fixture unavailable'); return; }
    if (request.url === '/api/read') { response.end('read'); return; }
    if (request.url === '/large') { response.setHeader('Content-Type', 'text/html'); response.end('x'.repeat(3 * 1024 * 1024)); return; }
    response.setHeader('Content-Type', 'text/html');
    response.end(`<!doctype html><html><head><title>DevCheck fixture</title></head><body>
      <h1 id="heading">Fixture ready</h1><button id="launch" aria-label="Launch check">Go</button>
      <div id="hidden" hidden>Hidden</div><div class="duplicate">one</div><div class="duplicate">two</div>
      <input type="password" value="do-not-capture-password"><div id="wide" style="width:2400px;height:1400px;background:linear-gradient(90deg,red,blue)">Large area</div>
      <script>
        console.warn('fixture warning');
        fetch('/missing').catch(()=>{});
        fetch('/api/read').catch(()=>{});
        fetch('/mutation', {method:'POST', body:'forbidden'}).catch(()=>{});
        fetch('http://127.0.0.1:${outsider.address().port}/escape').catch(()=>{});
        fetch('http://192.168.1.1:4200/private').catch(()=>{});
        fetch('https://example.org/external').catch(()=>{});
        new WebSocket('ws://127.0.0.1:${server.address().port}/socket');
      </script></body></html>`);
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  return { url: `http://127.0.0.1:${server.address().port}`, calls, close: async () => {
    server.closeAllConnections(); outsider.closeAllConnections();
    await Promise.all([new Promise(resolve => server.close(resolve)), new Promise(resolve => outsider.close(resolve))]);
  } };
}

test('real fixture inspect records bounded facts and blocks network escape/mutations', { skip: !browserAvailable }, async () => {
  const app = await fixture();
  const workspace = await mkdtemp(join(tmpdir(), 'devcheck-inspect-'));
  try {
    const result = await runDevCheck('inspect', { url: app.url, wait_ms: 250 }, { workspace });
    assert.equal(result.ok, true);
    assert.equal(result.verdict, 'observed');
    const evidence = JSON.parse(await readFile(join(workspace, result.evidence.path), 'utf8'));
    assert.equal(evidence.page.title, 'DevCheck fixture');
    assert.ok(evidence.dom.some(item => item.name === 'Launch check'));
    assert.ok(evidence.console.some(item => item.text === 'fixture warning'));
    assert.ok(evidence.http_errors.some(item => item.status === 503));
    assert.ok(evidence.blocked_requests.some(item => item.method === 'POST'));
    assert.ok(evidence.blocked_requests.some(item => item.kind === 'websocket'));
    assert.ok(!app.calls.some(item => item.startsWith('ESCAPE') || item.startsWith('POST')));
    assert.ok(!JSON.stringify(evidence).includes('do-not-capture-password'));
    assert.equal(evidence.policy.network, 'same-origin GET/HEAD only; application-level fence, not an OS sandbox');
    assert.ok(result.evidence.path.startsWith('.devcheck/'));
    assert.match(result.evidence.sha256, /^[a-f0-9]{64}$/);
  } finally { await app.close(); }
});

test('deterministic checks distinguish pass/fail and refuse ambiguous elements', { skip: !browserAvailable }, async () => {
  const app = await fixture();
  const workspace = await mkdtemp(join(tmpdir(), 'devcheck-check-'));
  try {
    const result = await runDevCheck('check', { url: app.url, checks: [
      { selector: '#heading', kind: 'text', expected: 'Fixture ready' },
      { selector: '#hidden', kind: 'visible', expected: false },
      { selector: '#absent', kind: 'exists', expected: false },
      { selector: '#launch', kind: 'visible', expected: true },
      { selector: '#launch', kind: 'text', expected: 'Wrong' },
      { selector: '.duplicate', kind: 'text', expected: 'one' },
    ] }, { workspace });
    assert.equal(result.ok, false);
    assert.equal(result.verdict, 'failed');
    assert.equal(result.error.code, 'CHECK_FAILED');
    const evidence = JSON.parse(await readFile(join(workspace, result.evidence.path), 'utf8'));
    assert.deepEqual(evidence.checks.map(check => check.passed), [true, true, true, true, false, false]);
    assert.equal(evidence.checks[5].reason, 'expected exactly one matching element; found 2');
    assert.equal(evidence.checks[4].actual, 'Go');
    const pass = await runDevCheck('check', { url: app.url, checks: [{ selector: '#heading', kind: 'text', expected: 'Fixture ready' }] }, { workspace });
    assert.equal(pass.ok, true); assert.equal(pass.verdict, 'passed');
    assert.notEqual(result.evidence.path, pass.evidence.path, 'never overwrite a prior receipt');
  } finally { await app.close(); }
});

test('captures page, element and explicit rectangle as capped hashed JPEG artifacts', { skip: !browserAvailable }, async () => {
  const app = await fixture();
  const workspace = await mkdtemp(join(tmpdir(), 'devcheck-capture-'));
  try {
    for (const target of [{ target: 'page', max_bytes: 16 * 1024 }, { target: 'selector', selector: '#wide' },
      { target: 'rectangle', rectangle: { x: 0, y: 0, width: 300, height: 180 } }]) {
      const result = await runDevCheck('capture', { url: app.url, ...target }, { workspace });
      assert.equal(result.ok, true, JSON.stringify(result));
      assert.equal(result.verdict, 'captured');
      assert.equal(result.artifacts.length, 1);
      const artifact = result.artifacts[0];
      assert.ok(artifact.width <= 1280 && artifact.height <= 960);
      assert.ok(artifact.bytes <= (target.max_bytes ?? 128 * 1024));
      assert.equal(artifact.mime, 'image/jpeg');
      assert.match(artifact.sha256, /^[a-f0-9]{64}$/);
      assert.equal(artifact.role, 'context-image');
      const bytes = await readFile(resolve(workspace, artifact.path));
      assert.equal(bytes[0], 0xff); assert.equal(bytes[1], 0xd8);
    }
  } finally { await app.close(); }
});

test('oversized responses and missing capture selectors stay unverified with no fabricated image', { skip: !browserAvailable }, async () => {
  const app = await fixture();
  const workspace = await mkdtemp(join(tmpdir(), 'devcheck-failure-'));
  try {
    const tooLarge = await runDevCheck('inspect', { url: app.url + '/large' }, { workspace });
    assert.equal(tooLarge.ok, false); assert.equal(tooLarge.verdict, 'unverified');
    const receipt = JSON.parse(await readFile(join(workspace, tooLarge.evidence.path), 'utf8'));
    assert.ok(receipt.blocked_requests.some(item => item.reason === 'response byte budget exceeded'));
    const missing = await runDevCheck('capture', { url: app.url, target: 'selector', selector: '#missing' }, { workspace });
    assert.equal(missing.ok, false); assert.equal(missing.verdict, 'unverified');
    assert.equal(missing.error.code, 'CAPTURE_TARGET'); assert.deepEqual(missing.artifacts, []);
  } finally { await app.close(); }
});

test('redirect to another loopback service is blocked and retains failure evidence', { skip: !browserAvailable }, async () => {
  const app = await fixture();
  const workspace = await mkdtemp(join(tmpdir(), 'devcheck-redirect-'));
  try {
    const result = await runDevCheck('inspect', { url: app.url + '/redirect' }, { workspace });
    assert.equal(result.ok, false);
    assert.equal(result.verdict, 'unverified');
    assert.ok(!app.calls.some(item => item.startsWith('ESCAPE')));
    assert.ok(result.evidence.path);
  } finally { await app.close(); }
});
