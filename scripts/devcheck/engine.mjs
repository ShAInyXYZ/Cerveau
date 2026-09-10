// Trusted, finite DevCheck procedures. Callers provide selectors/data, never code.
import { createHash, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, realpath } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer, request as httpRequest } from 'node:http';
import { once } from 'node:events';

const MAX_INPUT = 32 * 1024;
const LIMITS = Object.freeze({ events: 80, text: 300, dom: 80, requests: 200, responseBytes: 2 * 1024 * 1024, totalBytes: 8 * 1024 * 1024 });
const VIEWPORT = Object.freeze({ width: 1280, height: 960 });
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const short = (value, limit = LIMITS.text) => String(value).slice(0, limit);

export class DevCheckError extends Error {
  constructor(code, message) { super(message); this.name = code; this.code = code; }
}
const invalid = message => { throw new DevCheckError('INVALID_INPUT', message); };
const plainObject = value => value && typeof value === 'object' && !Array.isArray(value);
function keys(value, allowed, label) {
  if (!plainObject(value)) invalid(`${label} must be an object`);
  for (const key of Object.keys(value)) if (!allowed.includes(key)) invalid(`${label}: unsupported field ${key}`);
}
function integer(value, min, max, label) {
  if (!Number.isInteger(value) || value < min || value > max) invalid(`${label} must be an integer ${min}..${max}`);
  return value;
}
function selector(value) {
  if (typeof value !== 'string' || !value.trim() || value.length > 300) invalid('selector must be non-empty CSS, at most 300 characters');
  // CSS only: do not admit Playwright selector engines or arbitrary XPath/text.
  if (value.includes('>>') || /^(?:[a-zA-Z]+)=/.test(value)) invalid('selector must be CSS, not a selector engine expression');
  return value;
}

export function validateInput(action, input) {
  if (!['inspect', 'check', 'capture'].includes(action)) invalid('action must be inspect, check, or capture');
  const specific = action === 'check' ? ['checks'] : action === 'capture' ? ['target', 'selector', 'rectangle', 'max_bytes'] : [];
  keys(input, ['url', 'wait_ms', ...specific], action);
  if (JSON.stringify(input).length > MAX_INPUT) invalid('input exceeds 32 KiB');
  let url;
  try { url = new URL(input.url); } catch { /* validation below */ }
  // Validate the raw authority too: URL normalization otherwise admits 127.1,
  // decimal/hex addresses and implicit default ports as surprising aliases.
  if (typeof input.url !== 'string' || input.url.length > 2048 ||
    !/^http:\/\/(?:localhost|127\.0\.0\.1):[0-9]{4,5}(?:[/?]|$)/.test(input.url) ||
    !url || url.protocol !== 'http:' || !['localhost', '127.0.0.1'].includes(url.hostname) ||
    Number(url.port) < 1024 || Number(url.port) > 65535 || url.username || url.password || url.hash) {
    throw new DevCheckError('URL_SCOPE', 'use an explicit local app URL: http://localhost:PORT or http://127.0.0.1:PORT (1024..65535), without credentials or fragments');
  }
  const validated = { ...input, url: url.href, wait_ms: integer(input.wait_ms ?? 200, 0, 1500, 'wait_ms') };
  if (action === 'check') {
    if (!Array.isArray(input.checks) || input.checks.length < 1 || input.checks.length > 20) invalid('checks must contain 1..20 assertions');
    for (const check of input.checks) {
      keys(check, ['selector', 'kind', 'expected'], 'check'); selector(check.selector);
      if (!['exists', 'visible', 'text', 'count'].includes(check.kind)) invalid('check kind must be exists, visible, text, or count');
      if (['exists', 'visible'].includes(check.kind) && typeof check.expected !== 'boolean') invalid(`${check.kind} expected must be boolean`);
      if (check.kind === 'text' && (typeof check.expected !== 'string' || check.expected.length > 500)) invalid('text expected must be a string of at most 500 characters');
      if (check.kind === 'count') integer(check.expected, 0, 1000, 'count expected');
    }
  }
  if (action === 'capture') {
    if (!['page', 'selector', 'rectangle'].includes(input.target)) invalid('target must be page, selector, or rectangle');
    if (input.target === 'selector') selector(input.selector);
    else if ('selector' in input) invalid('selector is only valid for target selector');
    if (input.target === 'rectangle') {
      keys(input.rectangle, ['x', 'y', 'width', 'height'], 'rectangle');
      const rect = input.rectangle;
      integer(rect.x, 0, VIEWPORT.width - 1, 'rectangle.x'); integer(rect.y, 0, VIEWPORT.height - 1, 'rectangle.y');
      integer(rect.width, 1, VIEWPORT.width, 'rectangle.width'); integer(rect.height, 1, VIEWPORT.height, 'rectangle.height');
      if (rect.x + rect.width > VIEWPORT.width || rect.y + rect.height > VIEWPORT.height) invalid('rectangle must fit the 1280x960 viewport');
    } else if ('rectangle' in input) invalid('rectangle is only valid for target rectangle');
    validated.max_bytes = integer(input.max_bytes ?? 128 * 1024, 16 * 1024, 256 * 1024, 'max_bytes');
  }
  return validated;
}

export function requestAllowed(rawURL, method, origin) {
  try {
    const url = new URL(rawURL);
    return ['GET', 'HEAD'].includes(method) && url.protocol === 'http:' && url.origin === origin && !url.username && !url.password;
  } catch { return false; }
}

export function compactResult(result) {
  // Keep JSON structurally complete before the host's character ingress cap.
  // Full diagnostics are already in the receipt; artifact identities are never
  // truncated. Control characters/non-ASCII can expand considerably in JSON.
  if (Buffer.byteLength(JSON.stringify(result)) <= 5500) return result;
  result.summary_truncated = true;
  result.facts = { page: result.facts?.page ? { title: short(result.facts.page.title, 100), url: short(result.facts.page.url, 200) } : undefined };
  if (result.failed_checks) result.failed_checks = result.failed_checks.slice(0, 2).map(check => ({ kind: check.kind,
    selector: short(check.selector, 120), passed: false, expected: typeof check.expected === 'string' ? short(check.expected, 100) : check.expected,
    actual: typeof check.actual === 'string' ? short(check.actual, 100) : check.actual, reason: check.reason ? short(check.reason, 120) : undefined }));
  if (Buffer.byteLength(JSON.stringify(result)) > 5500) {
    result.facts = {}; delete result.failed_checks;
    if (result.error) result.error.message = short(result.error.message, 200);
  }
  return result;
}

function redactURL(raw) {
  try {
    const url = new URL(raw);
    if (!['http:', 'https:', 'ws:', 'wss:'].includes(url.protocol)) return `${url.protocol}[omitted]`;
    url.username = ''; url.password = ''; url.hash = '';
    for (const key of new Set(url.searchParams.keys())) url.searchParams.set(key, '[redacted]');
    return short(url.href, 500);
  } catch { return '[invalid URL]'; }
}

async function evidenceDestination(workspace) {
  const root = await realpath(workspace);
  const directory = join(root, '.devcheck');
  try { await mkdir(directory, { mode: 0o700 }); } catch (error) { if (error.code !== 'EEXIST') throw error; }
  const status = await lstat(directory);
  if (status.isSymbolicLink() || !status.isDirectory() || await realpath(directory) !== directory) {
    throw new DevCheckError('ARTIFACT_SCOPE', '.devcheck must be a real directory inside the session workspace, not a symlink');
  }
  const id = `${new Date().toISOString().replaceAll(/[:.]/g, '-')}-${randomUUID()}`;
  const path = join(directory, id);
  await mkdir(path, { mode: 0o700 }); // exclusive; never reuse or overwrite
  return { path, relative: `.devcheck/${id}` };
}

async function artifact(destination, name, bytes, metadata = {}) {
  // Fixed names only. Callers cannot supply an output path.
  const handle = await open(join(destination.path, name), constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
  try { await handle.writeFile(bytes); } finally { await handle.close(); }
  return { path: `${destination.relative}/${name}`, bytes: bytes.length, sha256: sha256(bytes), ...metadata };
}

// A second fence below Playwright routing: all proxied HTTP is connected to the
// approved loopback port literally, without DNS, credentials, redirects or TLS.
// This is NOT a substitute for an OS network namespace against browser exploits.
async function localProxy(origin, evidence, record) {
  const target = new URL(origin);
  let requestCount = 0; let totalBytes = 0;
  const inflight = new Set(); const sockets = new Set();
  const server = createServer((incoming, response) => {
    const allowed = requestAllowed(incoming.url, incoming.method, origin) && !incoming.headers['content-length'] && !incoming.headers['transfer-encoding'];
    if (!allowed || ++requestCount > LIMITS.requests || totalBytes >= LIMITS.totalBytes) {
      record('blocked_requests', { url: redactURL(incoming.url), method: incoming.method, kind: 'proxy', reason: allowed ? 'network budget exceeded' : 'outside local read-only origin' });
      response.writeHead(403); response.end('DevCheck request blocked'); return;
    }
    const url = new URL(incoming.url);
    const headers = { ...incoming.headers, host: target.host };
    for (const key of ['proxy-authorization', 'proxy-connection', 'authorization', 'cookie']) delete headers[key];
    const upstream = httpRequest({ hostname: '127.0.0.1', port: Number(target.port), path: url.pathname + url.search,
      method: incoming.method, headers, timeout: 8000 }, result => {
      const outHeaders = { ...result.headers };
      delete outHeaders['set-cookie']; delete outHeaders['connection'];
      response.writeHead(result.statusCode ?? 502, outHeaders);
      let bytes = 0;
      result.on('data', chunk => {
        bytes += chunk.length; totalBytes += chunk.length;
        if (bytes > LIMITS.responseBytes || totalBytes > LIMITS.totalBytes) {
          record('blocked_requests', { url: redactURL(incoming.url), method: incoming.method, kind: 'proxy', reason: 'response byte budget exceeded' });
          upstream.destroy(); response.destroy();
        } else response.write(chunk);
      });
      result.on('end', () => response.end());
      result.on('error', () => response.destroy());
    });
    inflight.add(upstream);
    upstream.on('close', () => inflight.delete(upstream));
    upstream.on('timeout', () => upstream.destroy(new Error('upstream timeout')));
    upstream.on('error', error => { if (!response.headersSent) response.writeHead(502); response.end();
      record('request_failures', { url: redactURL(incoming.url), error: short(error.message) }); });
    incoming.on('aborted', () => upstream.destroy());
    upstream.end();
  });
  const denySocket = (incoming, socket) => {
    record('blocked_requests', { url: redactURL(incoming.url), method: incoming.method, kind: 'proxy', reason: 'CONNECT and upgrades are not permitted' });
    socket.end('HTTP/1.1 403 Forbidden\r\nConnection: close\r\n\r\n');
  };
  server.on('connect', denySocket); server.on('upgrade', denySocket);
  server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  return { server: `http://127.0.0.1:${server.address().port}`, close: async () => {
    for (const request of inflight) request.destroy();
    for (const socket of sockets) socket.destroy();
    await new Promise(resolve => server.close(resolve));
    evidence.network_usage = { requests: requestCount, bytes: totalBytes };
  } };
}

async function collectPage(page, evidence) {
  evidence.page = { url: redactURL(page.url()), title: short(await page.title()), viewport: VIEWPORT };
  // Fixed implementation code, CSS queried with browser-native DOM APIs. Do not
  // collect values, hidden content, HTML, storage, cookies or arbitrary globals.
  evidence.dom = await page.evaluate(({ count, text }) => {
    return Array.from(document.querySelectorAll('h1,h2,h3,button,a,input,select,textarea,[role],[aria-label]')).slice(0, count).map(element => {
      const label = element.getAttribute('aria-label') || (element.labels ? Array.from(element.labels).map(item => item.textContent || '').join(' ') : '') ||
        (element.matches('input,textarea,select') ? '' : element.textContent || '');
      const box = element.getBoundingClientRect();
      return { tag: element.tagName.toLowerCase(), id: element.id.slice(0, 80), role: element.getAttribute('role') || '',
        name: label.trim().replace(/\s+/g, ' ').slice(0, text), visible: box.width > 0 && box.height > 0 && getComputedStyle(element).visibility !== 'hidden' };
    });
  }, { count: LIMITS.dom, text: 120 });
}

async function evaluateChecks(page, checks) {
  const results = [];
  for (const check of checks) {
    const item = { ...check, passed: false };
    try {
      // Explicit css= pins the selector engine; expressions are not code.
      const match = page.locator(`css=${check.selector}`);
      const count = await match.count();
      if (check.kind === 'exists') item.actual = count > 0;
      else if (check.kind === 'count') item.actual = count;
      else if (count !== 1) { item.reason = `expected exactly one matching element; found ${count}`; results.push(item); continue; }
      else if (check.kind === 'visible') item.actual = await match.isVisible();
      else item.actual = (await match.innerText({ timeout: 1000 })).trim().replace(/\s+/g, ' ');
      item.passed = item.actual === check.expected;
      if (typeof item.actual === 'string') item.actual = short(item.actual, 500);
    } catch (error) { item.reason = short(error.message); }
    results.push(item);
  }
  return results;
}

async function capture(page, browser, input, destination) {
  let bytes;
  const options = { type: 'jpeg', quality: 85, animations: 'disabled', caret: 'hide', scale: 'css', timeout: 8000 };
  if (input.target === 'selector') {
    const match = page.locator(`css=${input.selector}`);
    const count = await match.count();
    if (count !== 1) throw new DevCheckError('CAPTURE_TARGET', `selector must match exactly one element; found ${count}`);
    const box = await match.boundingBox();
    if (!box || box.width <= 0 || box.height <= 0) throw new DevCheckError('CAPTURE_TARGET', 'selector has no visible area');
    if (box.width > 4096 || box.height > 4096 || box.width * box.height > 8_000_000) throw new DevCheckError('CAPTURE_TARGET', 'element exceeds 4096px or 8 megapixels; choose a smaller selector');
    bytes = await match.screenshot(options);
  } else {
    bytes = await page.screenshot({ ...options, fullPage: false, ...(input.target === 'rectangle' ? { clip: input.rectangle } : {}) });
  }
  // Resizing happens in a separate blank context, never in the inspected app.
  // Input is an internal JPEG buffer, not an arbitrary URL or executable source.
  const encoder = await browser.newContext({ serviceWorkers: 'block' });
  try {
    await encoder.route('**/*', route => route.abort('blockedbyclient'));
    const canvasPage = await encoder.newPage();
    const encoded = await canvasPage.evaluate(async ({ data, maxBytes }) => {
      const img = new Image(); img.src = `data:image/jpeg;base64,${data}`; await img.decode();
      let scale = Math.min(1, 1280 / img.width, 960 / img.height);
      const canvas = document.createElement('canvas');
      for (let attempt = 0; attempt < 8; attempt++) {
        canvas.width = Math.max(1, Math.floor(img.width * scale)); canvas.height = Math.max(1, Math.floor(img.height * scale));
        const ctx = canvas.getContext('2d'); ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, canvas.width, canvas.height);
        ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
        for (const quality of [0.75, 0.6, 0.45]) {
          const dataURL = canvas.toDataURL('image/jpeg', quality); const base64 = dataURL.slice(dataURL.indexOf(',') + 1);
          const length = Math.floor(base64.length * 3 / 4) - (base64.endsWith('==') ? 2 : base64.endsWith('=') ? 1 : 0);
          if (length <= maxBytes) return { base64, width: canvas.width, height: canvas.height, quality, original_width: img.width, original_height: img.height };
        }
        scale *= 0.75;
      }
      throw Error('CAPTURE_BUDGET: screenshot could not fit the requested byte cap');
    }, { data: bytes.toString('base64'), maxBytes: input.max_bytes });
    const compressed = Buffer.from(encoded.base64, 'base64');
    if (compressed.length > input.max_bytes) throw new DevCheckError('CAPTURE_BUDGET', 'encoded screenshot exceeded the byte cap');
    const { base64, ...metadata } = encoded;
    return artifact(destination, 'capture.jpg', compressed, { mime: 'image/jpeg', role: 'context-image', ...metadata });
  } finally { await encoder.close(); }
}

export async function runDevCheck(action, rawInput, options = {}) {
  const input = validateInput(action, rawInput);
  const destination = await evidenceDestination(options.workspace ?? process.cwd());
  const started = Date.now();
  const evidence = { schema: 'cerveau.devcheck.v1', action, started_at: new Date(started).toISOString(),
    target: redactURL(input.url), policy: { network: 'same-origin GET/HEAD only; application-level fence, not an OS sandbox',
      browser: 'fresh isolated context; no credentials, persistent profile, CDP attach, downloads, service workers, WebSockets or caller scripts',
      viewport: VIEWPORT, limits: LIMITS, visual_correctness: 'not assessed', desktop_capture: false },
    console: [], page_errors: [], http_errors: [], request_failures: [], blocked_requests: [], truncated: {}, artifacts: [] };
  const record = (key, item) => {
    if (evidence[key].length < LIMITS.events) evidence[key].push(item);
    else evidence.truncated[key] = (evidence.truncated[key] ?? 0) + 1;
  };
  const result = { schema: 'cerveau.devcheck.v1', ok: false, action, verdict: 'unverified', artifacts: [] };
  let browser; let proxy; let deadline;
  try {
    let chromium;
    try { ({ chromium } = await import(process.env.CERVEAU_PLAYWRIGHT_MODULE || 'playwright')); }
    catch { throw new DevCheckError('DEPENDENCY_MISSING', 'Playwright is unavailable; configure CERVEAU_PLAYWRIGHT_MODULE or install the documented runtime before invoking DevCheck'); }
    proxy = await localProxy(new URL(input.url).origin, evidence, record);
    browser = await chromium.launch({ headless: true, executablePath: process.env.CERVEAU_CHROMIUM || undefined,
      proxy: { server: proxy.server, bypass: '<-loopback>' },
      args: ['--disable-quic', '--disable-background-networking', '--force-webrtc-ip-handling-policy=disable_non_proxied_udp', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'] });
    deadline = setTimeout(() => { result.error = { code: 'DEADLINE', message: 'DevCheck exceeded its 25 second browser deadline' }; void browser.close(); }, 25_000);
    const context = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: 1, serviceWorkers: 'block', acceptDownloads: false, permissions: [] });
    context.setDefaultTimeout(8000);
    if (typeof context.routeWebSocket !== 'function') throw new DevCheckError('DEPENDENCY_VERSION', 'Playwright 1.48 or newer is required for WebSocket blocking');
    await context.routeWebSocket(/.*/, socket => {
      record('blocked_requests', { url: redactURL(socket.url()), method: 'CONNECT', kind: 'websocket', reason: 'WebSockets are disabled' });
      socket.close();
    });
    await context.route('**/*', route => {
      const request = route.request();
      if (requestAllowed(request.url(), request.method(), new URL(input.url).origin)) return route.continue();
      record('blocked_requests', { url: redactURL(request.url()), method: request.method(), kind: 'browser', reason: 'outside local read-only origin' });
      return route.abort('blockedbyclient');
    });
    const page = await context.newPage();
    context.on('page', popup => { if (popup !== page) void popup.close(); });
    page.on('dialog', dialog => { void dialog.dismiss(); });
    page.on('download', download => { void download.cancel(); });
    page.on('console', message => { if (['warning', 'error'].includes(message.type())) record('console', { level: message.type(), text: short(message.text()) }); });
    page.on('pageerror', error => record('page_errors', { text: short(error.message) }));
    page.on('requestfailed', request => record('request_failures', { url: redactURL(request.url()), error: short(request.failure()?.errorText ?? 'unknown') }));
    page.on('response', response => { if (response.status() >= 400) record('http_errors', { url: redactURL(response.url()), status: response.status() }); });
    const response = await page.goto(input.url, { waitUntil: 'domcontentloaded', timeout: 8000 });
    if (!response || response.status() >= 400) throw new DevCheckError('NAVIGATION_FAILED', `top-level page returned ${response?.status() ?? 'no response'}`);
    if (input.wait_ms) await page.waitForTimeout(input.wait_ms);
    if (!requestAllowed(page.url(), 'GET', new URL(input.url).origin)) throw new DevCheckError('NAVIGATION_SCOPE', 'page left the approved origin');
    await collectPage(page, evidence);
    if (action === 'check') {
      evidence.checks = await evaluateChecks(page, input.checks);
      result.verdict = evidence.checks.every(check => check.passed) ? 'passed' : 'failed';
      result.ok = result.verdict === 'passed';
      result.checks = { passed: evidence.checks.filter(check => check.passed).length, total: evidence.checks.length };
      if (!result.ok) result.error = { code: 'CHECK_FAILED', message: `${result.checks.total - result.checks.passed} declared checks failed; inspect the evidence receipt` };
    } else if (action === 'capture') {
      evidence.artifacts.push(await capture(page, browser, input, destination));
      result.artifacts = evidence.artifacts; result.ok = true; result.verdict = 'captured';
    } else { result.ok = true; result.verdict = 'observed'; }
  } catch (error) {
    result.ok = false; result.verdict = 'unverified';
    result.error ??= { code: error.code ?? 'BROWSER_FAILED', message: short(error.message, 600) };
  } finally {
    clearTimeout(deadline);
    await browser?.close().catch(() => {});
    await proxy?.close();
  }
  evidence.finished_at = new Date().toISOString(); evidence.elapsed_ms = Date.now() - started;
  evidence.verdict = result.verdict; evidence.error = result.error;
  result.evidence = await artifact(destination, 'evidence.json', Buffer.from(JSON.stringify(evidence, null, 2) + '\n'), { mime: 'application/json' });
  result.observations = Object.fromEntries(['console', 'page_errors', 'http_errors', 'request_failures', 'blocked_requests'].map(key => [key, evidence[key].length]));
  // Small actionable context by default; the full receipt remains available.
  // A successful capture/inspection is not an application correctness verdict.
  result.facts = { page: evidence.page,
    labels: (evidence.dom ?? []).filter(item => item.visible && item.name).slice(0, 8).map(({ tag, id, name }) => ({ tag, id, name })),
    errors: [...evidence.page_errors.map(item => item.text), ...evidence.http_errors.map(item => `HTTP ${item.status} ${item.url}`)].slice(0, 4).map(item => short(item, 180)) };
  if (evidence.checks) result.failed_checks = evidence.checks.filter(check => !check.passed).slice(0, 4);
  return compactResult(result);
}
