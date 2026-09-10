// Trusted finite browser procedures. Inputs are data, never executable code.
import { createHash, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, realpath } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createServer, request as httpRequest } from 'node:http';
import { once } from 'node:events';

export const SCHEMA = 'cerveau.browsernative.v1';
export const MAX_INPUT = 32 * 1024;
export const LIMITS = Object.freeze({ steps: 20, events: 80, requests: 200, responseBytes: 2 * 1024 * 1024,
  totalBytes: 8 * 1024 * 1024, cpuNodes: 5000, cpuSamples: 10000, frames: 360, screenshots: 6, receiptBytes: 256 * 1024 });
const VIEWPORT = Object.freeze({ width: 1280, height: 960 });
const short = (value, limit = 300) => String(value).slice(0, limit);
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
export class BrowserNativeError extends Error {
  constructor(code, message) { super(message); this.name = code; this.code = code; }
}
const fail = (code, message) => { throw new BrowserNativeError(code, message); };
const invalid = message => fail('INVALID_INPUT', message);
function keys(value, allowed, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid(`${label} must be an object`);
  for (const key of Object.keys(value)) if (!allowed.includes(key)) invalid(`${label}: unsupported field ${key}`);
}
function integer(value, min, max, label) {
  if (!Number.isInteger(value) || value < min || value > max) invalid(`${label} must be an integer ${min}..${max}`);
  return value;
}
function selector(value) {
  if (typeof value !== 'string' || !value.trim() || value.length > 300 || value.includes('>>') || /^(?:[a-zA-Z]+)=/.test(value))
    invalid('selector must be non-empty CSS of at most 300 characters, without selector engine expressions');
  return value;
}
function condition(value) {
  keys(value, ['selector', 'kind', 'expected'], 'condition'); selector(value.selector);
  if (!['exists', 'visible', 'text', 'count'].includes(value.kind)) invalid('condition kind must be exists, visible, text, or count');
  if (['exists', 'visible'].includes(value.kind) && typeof value.expected !== 'boolean') invalid('exists/visible expected must be boolean');
  if (value.kind === 'text' && (typeof value.expected !== 'string' || value.expected.length > 500)) invalid('text expected must be at most 500 characters');
  if (value.kind === 'count') integer(value.expected, 0, 1000, 'count expected');
}
function localURL(raw) {
  let url; try { url = new URL(raw); } catch { /* rejected below */ }
  if (typeof raw !== 'string' || raw.length > 2048 || !/^http:\/\/(?:localhost|127\.0\.0\.1):[0-9]{4,5}(?:[/?]|$)/.test(raw) ||
      !url || url.protocol !== 'http:' || !['localhost', '127.0.0.1'].includes(url.hostname) ||
      Number(url.port) < 1024 || Number(url.port) > 65535 || url.username || url.password || url.hash)
    fail('URL_SCOPE', 'use explicit http://localhost:PORT or http://127.0.0.1:PORT (1024..65535), without credentials or fragments');
  return url.href;
}
const NAMED_KEYS = new Set(['Enter', 'Tab', 'Escape', 'Space', 'Backspace', 'Delete', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown', 'Shift', 'Control', 'Alt', 'Meta']);
export function validateInput(action, raw) {
  if (!['browser_run', 'runtime_profile'].includes(action)) invalid('action must be browser_run or runtime_profile');
  keys(raw, action === 'browser_run' ? ['url', 'steps', 'wait_ms', 'max_bytes'] : ['url', 'duration_ms', 'sampling_interval_us', 'max_hotspots'], action);
  if (Buffer.byteLength(JSON.stringify(raw)) > MAX_INPUT) invalid('input exceeds 32 KiB');
  const input = { ...raw, url: localURL(raw.url) };
  if (action === 'runtime_profile') return { ...input,
    duration_ms: integer(raw.duration_ms ?? 2000, 1000, 5000, 'duration_ms'),
    sampling_interval_us: integer(raw.sampling_interval_us ?? 1000, 1000, 10000, 'sampling_interval_us'),
    max_hotspots: integer(raw.max_hotspots ?? 10, 1, 20, 'max_hotspots') };
  input.wait_ms = integer(raw.wait_ms ?? 200, 0, 1500, 'wait_ms');
  input.max_bytes = integer(raw.max_bytes ?? 128 * 1024, 16 * 1024, 256 * 1024, 'max_bytes');
  if (!Array.isArray(raw.steps) || raw.steps.length < 1 || raw.steps.length > LIMITS.steps) invalid('steps must contain 1..20 entries');
  const ids = new Set(); let captures = 0;
  input.steps = raw.steps.map((step, index) => {
    const fields = { click: ['selector', 'button'], fill: ['selector', 'value'], key: ['key', 'action', 'hold_ms'],
      pointer: ['action', 'x', 'y', 'from_x', 'from_y', 'button', 'duration_ms'], wait: ['condition', 'timeout_ms'], check: ['condition'], capture: [] };
    if (!step || !Object.hasOwn(fields, step.type)) invalid('step type must be click, fill, key, pointer, wait, check, or capture');
    keys(step, ['id', 'type', 'capture', ...fields[step.type]], `step ${index + 1}`);
    const id = step.id ?? `step-${index + 1}`;
    if (typeof id !== 'string' || !/^[A-Za-z0-9_-]{1,40}$/.test(id) || ids.has(id)) invalid('step ids must be unique, 1..40 ASCII letters, digits, underscores or hyphens');
    ids.add(id);
    if (step.capture !== undefined && !['before', 'after', 'both'].includes(step.capture)) invalid('capture must be before, after, or both');
    if (step.type === 'capture' && step.capture !== undefined) invalid('capture step cannot also request before/after captures');
    captures += step.type === 'capture' ? 1 : step.capture === 'both' ? 2 : step.capture ? 1 : 0;
    const out = { ...step, id };
    if (['click', 'fill'].includes(step.type)) selector(step.selector);
    if (step.type === 'fill' && (typeof step.value !== 'string' || step.value.length > 2000)) invalid('fill value must be a string of at most 2000 characters');
    if ('button' in step && !['left', 'middle', 'right'].includes(step.button)) invalid('button must be left, middle, or right');
    if (step.type === 'key') {
      if (typeof step.key !== 'string' || !(NAMED_KEYS.has(step.key) || /^[!-~]$/.test(step.key))) invalid('key must be one printable ASCII character or a supported named key');
      out.action = step.action ?? 'press';
      if (!['press', 'hold', 'release'].includes(out.action)) invalid('key action must be press, hold, or release');
      if (out.action === 'release' && 'hold_ms' in step) invalid('release cannot specify hold_ms');
      if (out.action !== 'release') out.hold_ms = integer(step.hold_ms ?? (out.action === 'hold' ? 100 : 0), 0, 1000, 'hold_ms');
    }
    if (step.type === 'pointer') {
      if (!['move', 'drag'].includes(step.action)) invalid('pointer action must be move or drag');
      integer(step.x, 0, VIEWPORT.width - 1, 'x'); integer(step.y, 0, VIEWPORT.height - 1, 'y');
      out.duration_ms = integer(step.duration_ms ?? 100, 0, 1000, 'duration_ms');
      if (step.action === 'drag') { integer(step.from_x, 0, VIEWPORT.width - 1, 'from_x'); integer(step.from_y, 0, VIEWPORT.height - 1, 'from_y'); }
      else if ('from_x' in step || 'from_y' in step || 'button' in step) invalid('from_x/from_y/button require pointer drag');
    }
    if (['wait', 'check'].includes(step.type)) condition(step.condition);
    if (step.type === 'wait') out.timeout_ms = integer(step.timeout_ms ?? 2000, 100, 5000, 'timeout_ms');
    return out;
  });
  if (captures > LIMITS.screenshots) invalid('at most 6 viewport screenshots per run');
  return input;
}
export function requestAllowed(raw, method, origin) {
  try { const url = new URL(raw); return ['GET', 'HEAD'].includes(method) && url.protocol === 'http:' && url.origin === origin && !url.username && !url.password; }
  catch { return false; }
}
function redactURL(raw) {
  try { const url = new URL(raw); if (!['http:', 'https:', 'ws:', 'wss:'].includes(url.protocol)) return `${url.protocol}[omitted]`;
    url.username = ''; url.password = ''; url.hash = '';
    for (const key of new Set(url.searchParams.keys())) url.searchParams.set(key, '[redacted]');
    return short(url.href, 500);
  } catch { return '[invalid URL]'; }
}
async function bounded(promise, ms, code = 'TIMEOUT') {
  let timer;
  try { return await Promise.race([promise, new Promise((_, reject) => { timer = setTimeout(() => reject(new BrowserNativeError(code, `${code}: operation exceeded ${ms} ms`)), ms); })]); }
  finally { clearTimeout(timer); }
}
async function evidenceDestination(workspace) {
  if (typeof workspace !== 'string' || !isAbsolute(workspace)) invalid('trusted workspace must be an absolute path');
  const root = await realpath(workspace); const directory = join(root, '.devcheck');
  try { await mkdir(directory, { mode: 0o700 }); } catch (error) { if (error.code !== 'EEXIST') throw error; }
  const status = await lstat(directory);
  if (status.isSymbolicLink() || !status.isDirectory() || await realpath(directory) !== directory) fail('ARTIFACT_SCOPE', '.devcheck must be a real directory in the workspace, not a symlink');
  const id = `${new Date().toISOString().replaceAll(/[:.]/g, '-')}-${randomUUID()}`;
  const path = join(directory, id); await mkdir(path, { mode: 0o700 });
  return { path, relative: `.devcheck/${id}` };
}
async function artifact(destination, name, bytes, metadata = {}) {
  const handle = await open(join(destination.path, name), constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
  try { await handle.writeFile(bytes); } finally { await handle.close(); }
  return { path: `${destination.relative}/${name}`, bytes: bytes.length, sha256: hash(bytes), ...metadata };
}
// Independent network fence below routing: connect literally to the approved
// IPv4 loopback port. Never resolve caller hosts, follow redirects, or tunnel.
async function localProxy(origin, evidence, record) {
  const target = new URL(origin); let requests = 0; let totalBytes = 0;
  const inflight = new Set(); const sockets = new Set();
  const server = createServer((incoming, response) => {
    const allowed = requestAllowed(incoming.url, incoming.method, origin) && !incoming.headers['content-length'] && !incoming.headers['transfer-encoding'];
    if (!allowed || ++requests > LIMITS.requests || totalBytes >= LIMITS.totalBytes) {
      record('blocked_requests', { url: redactURL(incoming.url), method: incoming.method, kind: 'proxy', reason: allowed ? 'network budget exceeded' : 'outside local read-only origin' });
      response.writeHead(403); response.end('Browser request blocked'); return;
    }
    const url = new URL(incoming.url); const headers = { ...incoming.headers, host: target.host };
    for (const name of ['proxy-authorization', 'proxy-connection', 'authorization', 'cookie']) delete headers[name];
    // Refuse compressed upstream responses to keep the byte budget meaningful.
    headers['accept-encoding'] = 'identity';
    const upstream = httpRequest({ hostname: '127.0.0.1', port: Number(target.port), path: url.pathname + url.search,
      method: incoming.method, headers, timeout: 5000 }, result => {
      if (result.headers['content-encoding'] && result.headers['content-encoding'] !== 'identity') {
        record('blocked_requests', { url: redactURL(incoming.url), method: incoming.method, kind: 'proxy', reason: 'compressed response denied by byte budget' });
        result.destroy(); response.writeHead(502); response.end(); return;
      }
      const outHeaders = { ...result.headers }; delete outHeaders['set-cookie']; delete outHeaders.connection;
      response.writeHead(result.statusCode ?? 502, outHeaders); let bytes = 0;
      result.on('data', chunk => { bytes += chunk.length; totalBytes += chunk.length;
        if (bytes > LIMITS.responseBytes || totalBytes > LIMITS.totalBytes) {
          record('blocked_requests', { url: redactURL(incoming.url), method: incoming.method, kind: 'proxy', reason: 'response byte budget exceeded' });
          upstream.destroy(); response.destroy();
        } else if (!response.write(chunk)) result.pause();
      });
      response.on('drain', () => result.resume()); result.on('end', () => response.end()); result.on('error', () => response.destroy());
    });
    inflight.add(upstream); upstream.on('close', () => inflight.delete(upstream));
    upstream.on('timeout', () => upstream.destroy(new Error('upstream timeout')));
    upstream.on('error', error => { if (!response.headersSent) response.writeHead(502); response.end(); record('request_failures', { url: redactURL(incoming.url), error: short(error.message) }); });
    incoming.on('aborted', () => upstream.destroy()); response.on('close', () => upstream.destroy()); upstream.end();
  });
  const denySocket = (incoming, socket) => { record('blocked_requests', { url: redactURL(incoming.url), method: incoming.method, kind: 'proxy', reason: 'CONNECT and upgrades are disabled' }); socket.end('HTTP/1.1 403 Forbidden\r\nConnection: close\r\n\r\n'); };
  server.on('connect', denySocket); server.on('upgrade', denySocket);
  server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  return { server: `http://127.0.0.1:${server.address().port}`, close: async () => {
    for (const request of inflight) request.destroy(); for (const socket of sockets) socket.destroy();
    await new Promise(resolve => server.close(resolve)); evidence.network_usage = { requests, bytes: totalBytes };
  } };
}
async function capture(page, browser, destination, name, maxBytes) {
  const bytes = await page.screenshot({ type: 'jpeg', quality: 75, fullPage: false, animations: 'disabled', caret: 'hide', scale: 'css', timeout: 2500 });
  if (bytes.length <= maxBytes) return artifact(destination, name, bytes, { mime: 'image/jpeg', role: 'context-image', width: VIEWPORT.width, height: VIEWPORT.height });
  // Encode in a separate blank context so app scripts cannot touch the image.
  const encoder = await browser.newContext({ serviceWorkers: 'block', viewport: VIEWPORT });
  try {
    await encoder.route('**/*', route => route.abort('blockedbyclient')); const canvasPage = await encoder.newPage();
    const encoded = await bounded(canvasPage.evaluate(async ({ data, cap }) => {
      const img = new Image(); img.src = `data:image/jpeg;base64,${data}`; await img.decode();
      const canvas = document.createElement('canvas'); let scale = 1;
      for (let attempt = 0; attempt < 8; attempt++) {
        canvas.width = Math.max(1, Math.floor(img.width * scale)); canvas.height = Math.max(1, Math.floor(img.height * scale));
        const ctx = canvas.getContext('2d'); ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, canvas.width, canvas.height); ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
        for (const quality of [0.6, 0.45]) {
          const base64 = canvas.toDataURL('image/jpeg', quality).split(',')[1];
          if (Math.ceil(base64.length * 3 / 4) <= cap) return { base64, width: canvas.width, height: canvas.height, quality };
        }
        scale *= 0.75;
      }
      throw Error('screenshot cannot fit image budget');
    }, { data: bytes.toString('base64'), cap: maxBytes }), 2500);
    const compressed = Buffer.from(encoded.base64, 'base64'); if (compressed.length > maxBytes) fail('CAPTURE_BUDGET', 'encoded screenshot exceeds byte cap');
    const { base64, ...metadata } = encoded;
    return artifact(destination, name, compressed, { mime: 'image/jpeg', role: 'context-image', ...metadata });
  } finally { await bounded(encoder.close(), 1000).catch(() => {}); }
}
async function actualCondition(page, declared) {
  // Browser-native selectors and bounded text collection; no arbitrary evaluate.
  return bounded(page.evaluate(({ selector: css, kind, expected }) => {
    let nodes;
    try { nodes = document.querySelectorAll(css); } catch { return { passed: false, error: 'INVALID_SELECTOR', actual: null }; }
    const count = nodes.length;
    if (kind !== 'count' && count > 1) return { passed: false, error: 'AMBIGUOUS_TARGET', actual: { count } };
    let actual;
    if (kind === 'count') actual = count;
    else if (kind === 'exists') actual = count === 1;
    else if (!count) return { passed: false, error: 'TARGET_MISSING', actual: { count: 0 } };
    else if (kind === 'visible') { const box = nodes[0].getBoundingClientRect(); const style = getComputedStyle(nodes[0]); actual = box.width > 0 && box.height > 0 && style.visibility !== 'hidden' && style.display !== 'none'; }
    else {
      // innerText can allocate an entire enormous subtree. Refuse oversized DOM
      // text and stream text nodes with a fixed traversal/character budget.
      const walker = document.createTreeWalker(nodes[0], NodeFilter.SHOW_TEXT); let text = ''; let node; let visited = 0;
      while ((node = walker.nextNode())) { if (++visited > 1000 || node.length > 4096 || text.length + node.length > 4096) return { passed: false, error: 'TEXT_BUDGET', actual: null }; text += node.data; }
      actual = text.trim().replace(/\s+/g, ' ');
    }
    return { passed: actual === expected, actual: typeof actual === 'string' ? actual.slice(0, 500) : actual, ...(typeof actual === 'string' && actual.length > 500 ? { actual_truncated: true } : {}) };
  }, declared), 1200);
}
async function uniqueTarget(page, css) {
  const match = page.locator(`css=${css}`); const count = await bounded(match.count(), 1200);
  if (count > 1) fail('AMBIGUOUS_TARGET', `selector matched ${count} elements; exactly one required`);
  if (count === 0) fail('TARGET_MISSING', 'selector matched no elements');
  return match;
}
async function runSteps(page, browser, input, evidence, destination) {
  let assertions = 0; let pointer = { x: 0, y: 0 };
  for (const step of input.steps) {
    const started = Date.now(); const item = { id: step.id, type: step.type, status: 'unverified', elapsed_ms: 0, actual: null, evidence: [] };
    evidence.steps.push(item);
    const take = async phase => { const value = await capture(page, browser, destination, `${step.id}-${phase}.jpg`, input.max_bytes); evidence.artifacts.push(value); item.evidence.push(value.path); };
    try {
      if (step.capture === 'before' || step.capture === 'both') await take('before');
      if (step.type === 'click') { await (await uniqueTarget(page, step.selector)).click({ button: step.button ?? 'left', timeout: 2000 }); item.actual = { dispatched: 'click', selector: step.selector }; }
      if (step.type === 'fill') { await (await uniqueTarget(page, step.selector)).fill(step.value, { timeout: 2000 }); item.actual = { dispatched: 'fill', selector: step.selector, characters: step.value.length }; }
      if (step.type === 'key') {
        const key = step.key === 'Space' ? ' ' : step.key;
        if (step.action === 'release') await bounded(page.keyboard.up(key), 1200);
        else { try { await bounded(page.keyboard.down(key), 1200); await pause(step.hold_ms); } finally { await bounded(page.keyboard.up(key), 1200); } }
        item.actual = { dispatched: 'key', key: step.key, action: step.action, hold_ms: step.hold_ms ?? 0, released: true };
      }
      if (step.type === 'pointer') {
        const button = step.button ?? 'left'; const segments = Math.max(1, Math.min(20, Math.ceil(step.duration_ms / 25)));
        const startX = step.action === 'drag' ? step.from_x : pointer.x; const startY = step.action === 'drag' ? step.from_y : pointer.y;
        await bounded(page.mouse.move(startX, startY), 1200);
        try {
          if (step.action === 'drag') await bounded(page.mouse.down({ button }), 1200);
          for (let n = 1; n <= segments; n++) { await bounded(page.mouse.move(startX + (step.x - startX) * n / segments, startY + (step.y - startY) * n / segments), 1200); if (step.duration_ms) await pause(step.duration_ms / segments); }
        } finally { if (step.action === 'drag') await bounded(page.mouse.up({ button }), 1200); }
        pointer = { x: step.x, y: step.y };
        item.actual = { dispatched: 'pointer', action: step.action, x: step.x, y: step.y, pointer_lock: 'unsupported; absolute viewport coordinates only' };
      }
      if (step.type === 'check' || step.type === 'wait') {
        assertions++; const until = Date.now() + (step.type === 'wait' ? step.timeout_ms : 0); let actual;
        do { actual = await actualCondition(page, step.condition);
          if (actual.passed || ['AMBIGUOUS_TARGET', 'INVALID_SELECTOR', 'TEXT_BUDGET'].includes(actual.error) || step.type === 'check') break;
          if (Date.now() >= until) break; await pause(Math.min(50, until - Date.now()));
        } while (Date.now() <= until);
        item.actual = actual.actual; item.condition = step.condition; if (actual.actual_truncated) item.actual_truncated = true;
        if (!actual.passed) {
          const code = step.type === 'wait' && !['AMBIGUOUS_TARGET', 'INVALID_SELECTOR', 'TEXT_BUDGET'].includes(actual.error) ? 'WAIT_TIMEOUT' : actual.error ?? 'CHECK_FAILED';
          fail(code, code === 'CHECK_FAILED' ? 'declared DOM condition did not match' : `DOM condition: ${code}`);
        }
      }
      if (step.type === 'capture') { await take('viewport'); item.actual = { captured: 'viewport' }; }
      if (!requestAllowed(page.url(), 'GET', new URL(input.url).origin)) fail('NAVIGATION_SCOPE', 'page left the approved origin');
      if (step.capture === 'after' || step.capture === 'both') await take('after');
      item.status = ['check', 'wait'].includes(step.type) ? 'passed' : 'executed';
    } catch (error) {
      item.error = { code: error.code ?? 'STEP_FAILED', message: short(error.message, 300) };
      item.status = ['CHECK_FAILED', 'AMBIGUOUS_TARGET', 'INVALID_SELECTOR'].includes(error.code) ? 'failed' : 'unverified';
      if ((step.capture === 'after' || step.capture === 'both') && Date.now() - started < 4000) await take('failure').catch(() => {});
      item.elapsed_ms = Date.now() - started;
      return { ok: false, verdict: item.status, error: item.error, assertions };
    }
    item.elapsed_ms = Date.now() - started;
  }
  return { ok: true, verdict: assertions ? 'passed' : 'executed', assertions };
}
export function summarizeCPU(profile, maxHotspots = 10) {
  // CDP delivers the profile as one object. Bound before traversing/serializing;
  // raw profiles are never persisted or sent to the model.
  if (!profile || !Array.isArray(profile.nodes) || !Array.isArray(profile.samples) || !Array.isArray(profile.timeDeltas)) return { available: false, reason: 'CPU profile missing samples' };
  if (profile.nodes.length > LIMITS.cpuNodes || profile.samples.length > LIMITS.cpuSamples || profile.timeDeltas.length > LIMITS.cpuSamples)
    return { available: false, reason: 'CPU profile exceeded node/sample budget; discarded', truncated: true, raw_profile_retained: false };
  const nodes = new Map();
  for (const node of profile.nodes) nodes.set(node.id, { frame: node.callFrame ?? {}, count: 0, us: 0 });
  let measured = 0;
  for (let index = 0; index < profile.samples.length; index++) { const us = Number(profile.timeDeltas[index]); const node = nodes.get(profile.samples[index]);
    if (!Number.isFinite(us) || us < 0) continue; measured += us; if (node) { node.count++; node.us += us; }
  }
  const hotspots = [...nodes.values()].filter(node => node.count > 0).sort((a, b) => b.us - a.us).slice(0, maxHotspots).map(node => ({
    function: short(node.frame.functionName || '(anonymous)', 120), url: node.frame.url ? redactURL(node.frame.url) : null,
    line: Number.isInteger(node.frame.lineNumber) && node.frame.lineNumber >= 0 ? node.frame.lineNumber + 1 : null,
    column: Number.isInteger(node.frame.columnNumber) && node.frame.columnNumber >= 0 ? node.frame.columnNumber + 1 : null,
    self_ms: Math.round(node.us / 100) / 10, samples: node.count, sample_share: measured ? Math.round(node.us / measured * 10000) / 10000 : null }));
  return { available: profile.samples.length > 0, ...(profile.samples.length ? {} : { reason: 'No CPU samples collected' }),
    samples: profile.samples.length, nodes: profile.nodes.length, sampled_ms: Math.round(measured / 100) / 10,
    duration_ms: Number.isFinite(profile.endTime - profile.startTime) ? Math.round((profile.endTime - profile.startTime) / 100) / 10 : null,
    hotspots, truncated: false, raw_profile_retained: false };
}
async function profilePage(page, context, input, evidence) {
  const cdp = await context.newCDPSession(page);
  const binding = `__cerveau_${randomUUID().replaceAll('-', '')}`;
  const collected = { installed: false, supported: [], longtasks: [], resources: [], frames: [], truncated: {} };
  let rendererPaused = false;
  cdp.on('Debugger.paused', () => { rendererPaused = true; });
  const navigation = { committed: false, dom_ready: false, status: null };
  page.on('domcontentloaded', () => { navigation.dom_ready = true; });
  cdp.on('Runtime.bindingCalled', event => {
    if (event.name !== binding || typeof event.payload !== 'string' || event.payload.length > 2000) return;
    let item; try { item = JSON.parse(event.payload); } catch { return; }
    if (item.kind === 'installed') { collected.installed = true; collected.supported = Array.isArray(item.supported) ? item.supported.filter(value => ['longtask', 'resource'].includes(value)) : []; return; }
    if (item.kind === 'capped' && ['longtasks', 'resources', 'frames'].includes(item.metric)) { collected.truncated[item.metric] = true; return; }
    if (!['longtasks', 'resources', 'frames'].includes(item.kind)) return;
    const list = collected[item.kind]; const cap = item.kind === 'frames' ? LIMITS.frames : LIMITS.events;
    if (list.length >= cap) { collected.truncated[item.kind] = true; return; }
    if (!Number.isFinite(item.duration) || item.duration < 0) return;
    list.push({ duration_ms: Math.round(item.duration * 10) / 10,
      ...(item.kind === 'resources' ? { url: redactURL(item.name), initiator: short(item.initiator ?? '', 40), transfer_bytes: Number.isFinite(item.transfer) ? item.transfer : null } : {}) });
  });
  await cdp.send('Runtime.enable'); await cdp.send('Runtime.addBinding', { name: binding });
  // Fixed source, bounded observers. Binding identifiers are generated internally.
  await page.addInitScript(({ bindingName, frameCap, eventCap }) => {
    if (window.top !== window) return;
    const send = item => { try { globalThis[bindingName](JSON.stringify(item)); } catch { /* CDP unavailable */ } };
    const supported = typeof PerformanceObserver === 'function' ? PerformanceObserver.supportedEntryTypes : [];
    send({ kind: 'installed', supported });
    for (const [type, kind] of [['longtask', 'longtasks'], ['resource', 'resources']]) {
      if (!supported.includes(type)) continue; let count = 0;
      const observer = new PerformanceObserver(list => { for (const entry of list.getEntries()) { if (++count > eventCap) { send({ kind: 'capped', metric: kind }); observer.disconnect(); return; }
        send({ kind, duration: entry.duration, name: entry.name.slice(0, 500), initiator: entry.initiatorType, transfer: entry.transferSize });
      } }); observer.observe({ type, buffered: true });
    }
    let previous; let frames = 0;
    const frame = timestamp => { if (previous !== undefined) send({ kind: 'frames', duration: timestamp - previous }); previous = timestamp;
      if (++frames < frameCap + 1) requestAnimationFrame(frame); else send({ kind: 'capped', metric: 'frames' });
    }; requestAnimationFrame(frame);
  }, { bindingName: binding, frameCap: LIMITS.frames, eventCap: LIMITS.events });
  await cdp.send('Debugger.enable');
  await cdp.send('Profiler.enable'); await cdp.send('Profiler.setSamplingInterval', { interval: input.sampling_interval_us });
  await cdp.send('Profiler.start'); const started = Date.now();
  // Start the host timer independently of navigation/page JavaScript. A busy
  // renderer cannot prevent Profiler.stop from being sent by the Node process.
  const navigate = page.goto(input.url, { waitUntil: 'commit', timeout: 7000 }).then(response => {
    navigation.committed = Boolean(response); navigation.status = response?.status() ?? null;
  }).catch(error => { navigation.error = short(error.message, 200); });
  await pause(input.duration_ms);
  let cpu; let paused = false;
  // Debugger.pause interrupts V8 even inside an infinite JavaScript loop.
  // Profiler.stop alone can queue behind that loop on some Chromium versions.
  // This only freezes the fresh profiling page at the end of its sample window.
  try { await bounded(cdp.send('Debugger.pause'), 1500, 'PROFILE_PAUSE_TIMEOUT'); paused = true; } catch { /* try stop even if pause is unavailable */ }
  try { const stopped = await bounded(cdp.send('Profiler.stop'), 4000, 'PROFILE_STOP_TIMEOUT'); cpu = summarizeCPU(stopped.profile, input.max_hotspots); }
  catch (error) { cpu = { available: false, reason: short(error.message), raw_profile_retained: false }; }
  await bounded(navigate, 1000, 'NAVIGATION_PENDING').catch(error => { navigation.error ??= short(error.message); });
  const stats = list => list.length ? { count: list.length, total_ms: Math.round(list.reduce((sum, item) => sum + item.duration_ms, 0) * 10) / 10,
    max_ms: Math.max(...list.map(item => item.duration_ms)) } : { count: 0, total_ms: 0, max_ms: null };
  const observed = kind => collected.installed && collected.supported.includes(kind);
  const profile = { requested_duration_ms: input.duration_ms, elapsed_ms: Date.now() - started, sampling_interval_us: input.sampling_interval_us,
    navigation, cpu, debugger_pause_requested: paused, renderer_paused_at_end: rendererPaused,
    long_tasks: observed('longtask') && (navigation.dom_ready || collected.longtasks.length) ? { available: true, ...stats(collected.longtasks), coverage: 'completed tasks delivered during sampling; a still-running task is not reported', events: collected.longtasks } : { available: false, reason: 'long-task observer unavailable or no completed tasks delivered before page initialization; a running task is not reported' },
    frames: collected.frames.length ? { available: true, ...stats(collected.frames), intervals_ms: collected.frames.map(item => item.duration_ms), coverage: 'requestAnimationFrame intervals; not compositor FPS' } : { available: false, reason: 'no frame callbacks observed; renderer may be blocked or not initialized' },
    resources: observed('resource') && (navigation.dom_ready || collected.resources.length) ? { available: true, count: collected.resources.length, entries: collected.resources, coverage: 'completed resource timing entries; not all network requests' } : { available: false, reason: 'resource observer unavailable or no completed entries delivered before page initialization' },
    truncated: collected.truncated, application_correctness: 'not assessed; profiling does not prove the application works' };
  evidence.profile = profile;
  if (!navigation.committed || navigation.status >= 400 || !cpu.available) return { ok: false, verdict: 'unverified', error: { code: 'PROFILE_UNVERIFIED', message: !cpu.available ? cpu.reason : 'local navigation did not commit successfully' } };
  return { ok: true, verdict: 'profiled' };
}
export function compactResult(result) {
  // Keep all per-step statuses and artifact identities; detailed observations
  // remain in the receipt. Keep complete JSON below the host's ingress cap.
  if (Buffer.byteLength(JSON.stringify(result)) <= 7000) return result;
  result.summary_truncated = true;
  result.steps = result.steps?.map(({ id, type, status, elapsed_ms, evidence, error }) => ({ id, type, status, elapsed_ms, evidence, ...(error ? { error: { code: error.code, message: short(error.message, 120) } } : {}) }));
  if (result.profile) result.profile = { requested_duration_ms: result.profile.requested_duration_ms, elapsed_ms: result.profile.elapsed_ms,
    navigation: result.profile.navigation, cpu: { available: result.profile.cpu.available, samples: result.profile.cpu.samples,
      truncated: result.profile.cpu.truncated, hotspots: result.profile.cpu.hotspots?.slice(0, 3), reason: result.profile.cpu.reason },
    application_correctness: result.profile.application_correctness, details: 'see evidence receipt' };
  if (Buffer.byteLength(JSON.stringify(result)) > 7000) {
    result.steps = result.steps?.map(({ id, type, status, elapsed_ms, evidence, error }) => ({ id, type, status, elapsed_ms, evidence,
      ...(error ? { error: { code: error.code } } : {}) }));
    if (result.profile?.cpu?.hotspots) result.profile.cpu.hotspots = result.profile.cpu.hotspots.map(item => ({ ...item, url: short(item.url, 120), function: short(item.function, 60) }));
    if (result.error) result.error.message = short(result.error.message, 120);
  }
  if (Buffer.byteLength(JSON.stringify(result)) > 7000) {
    // A receipt retains every per-step artifact reference; the summary retains
    // the complete artifact manifest without repeating long paths per step.
    result.steps = result.steps?.map(({ evidence, ...item }) => ({ ...item, evidence_count: evidence?.length ?? 0 }));
  }
  return result;
}
export async function runBrowserNative(action, rawInput, options = {}) {
  const input = validateInput(action, rawInput); const destination = await evidenceDestination(options.workspace);
  const started = Date.now(); const origin = new URL(input.url).origin;
  const evidence = { schema: SCHEMA, action, started_at: new Date(started).toISOString(), target: redactURL(input.url),
    policy: { network: 'same-origin GET/HEAD only; application-level fence, not an OS sandbox',
      browser: 'fresh isolated context; no credentials, user profile, CDP attach, downloads, service workers, WebSockets or caller scripts',
      viewport: VIEWPORT, limits: LIMITS, application_correctness: 'only declared DOM checks assessed; profiling does not assess correctness',
      text_conditions: 'normalized bounded DOM textContent, not rendered text', pointer_lock: 'unsupported', desktop_capture: false },
    console: [], page_errors: [], http_errors: [], request_failures: [], blocked_requests: [], truncated: {}, steps: [], artifacts: [] };
  const record = (key, item) => { if (evidence[key].length < LIMITS.events) evidence[key].push(item); else evidence.truncated[key] = true; };
  let browser; let proxy; let closing = false; let timer;
  const result = { schema: SCHEMA, action, ok: false, verdict: 'unverified' };
  try {
    const work = async () => {
      let chromium;
      const modulePath = process.env.CERVEAU_PLAYWRIGHT_MODULE;
      if (!modulePath || !isAbsolute(modulePath)) fail('DEPENDENCY_MISSING', 'configure trusted CERVEAU_PLAYWRIGHT_MODULE with an existing absolute Playwright entry path');
      if (process.env.CERVEAU_CHROMIUM && !isAbsolute(process.env.CERVEAU_CHROMIUM)) fail('DEPENDENCY_MISSING', 'CERVEAU_CHROMIUM must be an absolute executable path');
      try { ({ chromium } = await import(pathToFileURL(modulePath).href)); } catch { fail('DEPENDENCY_MISSING', 'configured Playwright module is unavailable'); }
      proxy = await localProxy(origin, evidence, record);
      browser = await chromium.launch({ headless: true, timeout: 8000, executablePath: process.env.CERVEAU_CHROMIUM || undefined,
        proxy: { server: proxy.server, bypass: '<-loopback>' },
        args: ['--disable-quic', '--disable-background-networking', '--force-webrtc-ip-handling-policy=disable_non_proxied_udp', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'] });
      if (closing) { await browser.close(); fail('DEADLINE', 'browser started after deadline'); }
      const context = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: 1, serviceWorkers: 'block', acceptDownloads: false, permissions: [] });
      context.setDefaultTimeout(2000);
      if (typeof context.routeWebSocket !== 'function') fail('DEPENDENCY_VERSION', 'Playwright 1.48 or newer is required for WebSocket blocking');
      await context.routeWebSocket(/.*/, socket => { record('blocked_requests', { url: redactURL(socket.url()), method: 'CONNECT', kind: 'websocket', reason: 'WebSockets are disabled' }); socket.close(); });
      await context.route('**/*', route => { const request = route.request(); if (requestAllowed(request.url(), request.method(), origin)) return route.continue();
        record('blocked_requests', { url: redactURL(request.url()), method: request.method(), kind: 'browser', reason: 'outside local read-only origin' }); return route.abort('blockedbyclient'); });
      const page = await context.newPage(); context.on('page', popup => { if (popup !== page) void popup.close().catch(() => {}); });
      page.on('dialog', dialog => { void dialog.dismiss().catch(() => {}); }); page.on('download', download => { void download.cancel().catch(() => {}); });
      page.on('console', message => { if (['warning', 'error'].includes(message.type())) record('console', { level: message.type(), text: short(message.text()) }); });
      page.on('pageerror', error => record('page_errors', { text: short(error.message) }));
      page.on('requestfailed', request => record('request_failures', { url: redactURL(request.url()), error: short(request.failure()?.errorText ?? 'unknown') }));
      page.on('response', response => { if (response.status() >= 400) record('http_errors', { url: redactURL(response.url()), status: response.status() }); });
      if (action === 'runtime_profile') return profilePage(page, context, input, evidence);
      const response = await page.goto(input.url, { waitUntil: 'domcontentloaded', timeout: 7000 });
      if (!response || response.status() >= 400) fail('NAVIGATION_FAILED', `top-level page returned ${response?.status() ?? 'no response'}`);
      if (input.wait_ms) await pause(input.wait_ms);
      if (!requestAllowed(page.url(), 'GET', origin)) fail('NAVIGATION_SCOPE', 'page left approved origin');
      return runSteps(page, browser, input, evidence, destination);
    };
    const outcome = await Promise.race([work(), new Promise((_, reject) => { timer = setTimeout(() => reject(new BrowserNativeError('DEADLINE', 'browser runtime exceeded its 24 second deadline')), 24000); })]);
    Object.assign(result, outcome);
  } catch (error) { result.error = { code: error.code ?? 'BROWSER_FAILED', message: short(error.message, 500) }; }
  finally { closing = true; clearTimeout(timer); await bounded(browser?.close() ?? Promise.resolve(), 2000).catch(() => {}); await proxy?.close(); }
  evidence.finished_at = new Date().toISOString(); evidence.elapsed_ms = Date.now() - started; evidence.verdict = result.verdict; evidence.error = result.error;
  // A global deadline may interrupt a step before its local catch executes.
  for (const step of evidence.steps) if (step.status === 'unverified' && !step.error) { step.error = result.error; step.elapsed_ms = evidence.elapsed_ms; }
  let bytes = Buffer.from(JSON.stringify(evidence));
  if (bytes.length > LIMITS.receiptBytes) { evidence.truncated.receipt = true;
    for (const key of ['console', 'page_errors', 'http_errors', 'request_failures', 'blocked_requests']) evidence[key] = evidence[key].slice(0, 10);
    if (evidence.profile?.resources?.entries) evidence.profile.resources.entries = evidence.profile.resources.entries.slice(0, 10);
    bytes = Buffer.from(JSON.stringify(evidence));
  }
  result.evidence = await artifact(destination, 'evidence.json', bytes, { mime: 'application/json' });
  result.elapsed_ms = evidence.elapsed_ms; result.steps = evidence.steps; result.artifacts = evidence.artifacts;
  result.observations = Object.fromEntries(['console', 'page_errors', 'http_errors', 'request_failures', 'blocked_requests'].map(key => [key, evidence[key].length]));
  if (evidence.profile) result.profile = { ...evidence.profile, frames: { ...evidence.profile.frames, intervals_ms: undefined }, resources: { ...evidence.profile.resources, entries: evidence.profile.resources.entries?.slice(0, 5) }, long_tasks: { ...evidence.profile.long_tasks, events: undefined } };
  return compactResult(result);
}
