import { expect, test } from 'vitest';
import { parseRfxArgs, canAutoRun, RfxRequestScope, contextImages } from './rfxArgs';

test('typed fields parse JSON arrays/objects and numbers without turning blanks into zero', () => {
  const schema = { type: 'object', properties: { paths: { type: 'array', items: { type: 'string' } }, options: { type: 'object' }, count: { type: 'integer' }, force: { type: 'boolean' } }, required: ['paths'] };
  expect(parseRfxArgs(schema, { paths: '["src/a.ts","src/b.ts"]', options: '{"dry":true}', count: '', force: false })).toEqual({ paths: ['src/a.ts', 'src/b.ts'], options: { dry: true }, force: false });
  expect(parseRfxArgs(schema, { paths: [], count: '0' })).toEqual({ paths: [], count: 0 });
  expect(() => parseRfxArgs(schema, { paths: 'not JSON' })).toThrow('paths: enter valid JSON');
  expect(() => parseRfxArgs(schema, { paths: '{}' })).toThrow('paths: expected an array');
  expect(() => parseRfxArgs(schema, { paths: '[]', options: '[]' })).toThrow('options: expected an object');
  expect(() => parseRfxArgs(schema, { paths: '[]', count: '1.2' })).toThrow('count: expected an integer');
});

test('required fields, false, zero, enum types and preset arguments retain their meaning', () => {
  const schema = { properties: { choice: { type: 'integer', enum: [0, 2] }, enabled: { type: 'boolean' }, limit: { type: 'number', minimum: 1 } }, required: ['choice', 'enabled'] };
  expect(parseRfxArgs(schema, { choice: '0', enabled: 'false' })).toEqual({ choice: 0, enabled: false });
  expect(parseRfxArgs(schema, { choice: '', enabled: '' }, { choice: 2, enabled: false })).toEqual({ choice: 2, enabled: false });
  expect(() => parseRfxArgs(schema, { choice: '', enabled: false })).toThrow('choice: required');
  expect(() => parseRfxArgs(schema, { choice: '3', enabled: false })).toThrow('choice: select an allowed value');
  expect(() => parseRfxArgs(schema, { choice: '2', enabled: 'no' })).toThrow('enabled: choose true or false');
  expect(() => parseRfxArgs(schema, { choice: '2', enabled: false, limit: '0' })).toThrow('limit: minimum is 1');
});

test('nested JSON values are validated before dispatch and unknown parameters cannot escape', () => {
  const schema = { additionalProperties: false, properties: { checks: { type: 'array', minItems: 1, items: { type: 'object', additionalProperties: false, properties: { kind: { enum: ['visible'] }, expected: { type: 'boolean' } }, required: ['kind', 'expected'] } } } };
  expect(() => parseRfxArgs(schema, { checks: '[{"kind":"visible","expected":"true"}]' })).toThrow('checks[0].expected: expected boolean');
  expect(() => parseRfxArgs(schema, { checks: '[]' })).toThrow('checks: at least 1 item');
  expect(parseRfxArgs(schema, { unrelated: 'ignored field from another talent', checks: '[{"kind":"visible","expected":true}]' })).toEqual({ checks: [{ kind: 'visible', expected: true }] });
  expect(() => parseRfxArgs({ properties: { object: { type: 'object', required: ['toString'] } } }, { object: '{}' })).toThrow('object.toString: required');
});

test('automatic status is safe, enabled, visible and idle only; missing required arguments never fire', () => {
  const safe = { name: 'status', risk: 'safe', enabled: true, params: {} };
  const state = { sessionId: 'a', busy: false, pending: false, hidden: false, open: true };
  expect(canAutoRun(safe, state)).toBe(true);
  for (const patch of [{ risk: 'dangerous' }, { risk: 'sensitive' }, { enabled: false }, { params: { required: ['path'] } }]) expect(canAutoRun({ ...safe, ...patch }, state)).toBe(false);
  for (const patch of [{ sessionId: null }, { busy: true }, { pending: true }, { hidden: true }, { open: false }]) expect(canAutoRun(safe, { ...state, ...patch })).toBe(false);
});

test('a shared lease blocks concurrent requests and rejects stale completions across switches/unmount', () => {
  const first = new RfxRequestScope(); const other = new RfxRequestScope();
  const token = first.begin('session-one'); expect(token).not.toBeNull();
  expect(other.begin('session-one')).toBeNull();
  expect(first.current(token!, 'session-one')).toBe(true);
  first.invalidate();
  expect(first.current(token!, 'session-one')).toBe(false);
  expect(first.current(token!, 'session-two')).toBe(false);
  expect(other.begin('session-one')).toBeNull(); // accepted work has not finished
  first.finish(token!);
  const next = other.begin('session-one'); expect(next).not.toBeNull();
  other.dispose(); expect(other.current(next!, 'session-one')).toBe(false);
  other.finish(next!); expect(other.begin('session-two')).toBeNull();
});

test('only successful typed DevCheck captures offer bounded relative image references', () => {
  const image = { role: 'context-image', mime: 'image/jpeg', path: '.devcheck/run/capture.jpg', sha256: 'a'.repeat(64), bytes: 8000, width: 640, height: 480 };
  const envelope = { schema: 'cerveau.devcheck.v1', ok: true, verdict: 'captured', artifacts: [image] };
  expect(contextImages(JSON.stringify(envelope))).toEqual([image]);
  expect(contextImages(JSON.stringify({ ...envelope, ok: false }))).toEqual([]);
  expect(contextImages(JSON.stringify({ ...envelope, artifacts: [{ ...image, path: '../../secret.jpg' }] }))).toEqual([]);
  expect(contextImages(JSON.stringify({ ...envelope, artifacts: [{ ...image, bytes: 9000000 }] }))).toEqual([]);
  expect(contextImages('ordinary tool output')).toEqual([]);
});
