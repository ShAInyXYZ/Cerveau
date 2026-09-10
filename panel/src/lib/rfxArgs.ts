// Shared form/coordinator rules for the cockpit and its complete talent list.
type Schema = Record<string, any>;
export function rfxParams(schema: Schema = {}): Array<Schema & { name: string; isRequired: boolean }> {
  const required = new Set(schema.required ?? []);
  return Object.entries(schema.properties ?? {}).map(([name, value]) => ({ ...(value as Schema), name, isRequired: required.has(name) }));
}
const fail = (path: string, reason: string): never => { throw new Error(`${path}: ${reason}`); };
const blank = (value: unknown) => value === undefined || value === null || typeof value === 'string' && value.trim() === '';

function validate(schema: Schema, value: any, path: string): void {
  if (schema.anyOf || schema.oneOf) {
    const choices = schema.anyOf ?? schema.oneOf;
    const matches = choices.filter((choice: Schema) => { try { validate(choice, value, path); return true; } catch { return false; } }).length;
    if (!matches || schema.oneOf && matches !== 1) fail(path, 'value does not match the allowed JSON types');
  }
  if (schema.enum && !schema.enum.some((entry: unknown) => JSON.stringify(entry) === JSON.stringify(value))) fail(path, 'select an allowed value');
  if (schema.type === 'array') {
    if (!Array.isArray(value)) fail(path, 'expected an array');
    if (schema.minItems !== undefined && value.length < schema.minItems) fail(path, `at least ${schema.minItems} item(s) required`);
    if (schema.maxItems !== undefined && value.length > schema.maxItems) fail(path, `at most ${schema.maxItems} items allowed`);
    if (schema.items) value.forEach((item: unknown, index: number) => validate(schema.items, item, `${path}[${index}]`));
  } else if (schema.type === 'object') {
    if (!value || typeof value !== 'object' || Array.isArray(value)) fail(path, 'expected an object');
    for (const name of schema.required ?? []) if (!Object.hasOwn(value, name)) fail(`${path}.${name}`, 'required');
    for (const [name, item] of Object.entries(value)) {
      if (schema.properties?.[name]) validate(schema.properties[name], item, `${path}.${name}`);
      else if (schema.additionalProperties === false) fail(`${path}.${name}`, 'unknown field');
    }
  } else if (schema.type === 'boolean' && typeof value !== 'boolean') fail(path, 'expected boolean');
  else if (schema.type === 'string') {
    if (typeof value !== 'string') fail(path, 'expected a string');
    if (schema.minLength !== undefined && value.length < schema.minLength) fail(path, `at least ${schema.minLength} characters required`);
    if (schema.maxLength !== undefined && value.length > schema.maxLength) fail(path, `at most ${schema.maxLength} characters allowed`);
  } else if (schema.type === 'integer' || schema.type === 'number') {
    if (typeof value !== 'number' || !Number.isFinite(value)) fail(path, 'expected a finite number');
    if (schema.type === 'integer' && !Number.isInteger(value)) fail(path, 'expected an integer');
    if (schema.minimum !== undefined && value < schema.minimum) fail(path, `minimum is ${schema.minimum}`);
    if (schema.maximum !== undefined && value > schema.maximum) fail(path, `maximum is ${schema.maximum}`);
  }
}

export function parseRfxArgs(schema: Schema = {}, fields: Record<string, any> = {}, preset: Record<string, any> = {}) {
  const entries: [string, unknown][] = [];
  for (const param of rfxParams(schema)) {
    const field = Object.hasOwn(fields, param.name) ? fields[param.name] : undefined;
    let value = blank(field) ? (Object.hasOwn(preset, param.name) ? preset[param.name] : undefined) : field;
    if (blank(value)) { if (param.isRequired) fail(param.name, 'required'); continue; }
    if (param.enum && typeof value === 'string') value = param.enum.find((item: unknown) => String(item) === value) ?? value;
    if (param.type === 'array' || param.type === 'object') {
      if (typeof value === 'string') { try { value = JSON.parse(value); } catch { fail(param.name, 'enter valid JSON'); } }
    } else if (param.type === 'integer' || param.type === 'number') {
      if (typeof value === 'string') value = Number(value);
    } else if (param.type === 'boolean' && typeof value !== 'boolean') {
      if (value === 'true') value = true;
      else if (value === 'false') value = false;
      else fail(param.name, 'choose true or false');
    }
    validate(param, value, param.name);
    entries.push([param.name, value]);
  }
  return Object.fromEntries(entries);
}

export function canAutoRun(target: any, state: { sessionId: string | null; busy: boolean; pending: boolean; hidden: boolean; open: boolean }) {
  return !!target && target.enabled === true && target.risk === 'safe' && !(target.params?.required?.length)
    && !!state.sessionId && !state.busy && !state.pending && !state.hidden && state.open;
}

type RequestToken = { sessionId: string; epoch: number; id: symbol };
const pendingSessions = new Map<string, symbol>();
export class RfxRequestScope {
  private epoch = 0;
  private disposed = false;
  begin(sessionId: string | null): RequestToken | null {
    if (!sessionId || this.disposed || pendingSessions.has(sessionId)) return null;
    const id = Symbol('RFX request'); pendingSessions.set(sessionId, id);
    return { sessionId, epoch: this.epoch, id };
  }
  current(token: RequestToken, sessionId: string | null) { return !this.disposed && token.epoch === this.epoch && token.sessionId === sessionId; }
  finish(token: RequestToken) { if (pendingSessions.get(token.sessionId) === token.id) pendingSessions.delete(token.sessionId); }
  invalidate() { this.epoch++; }
  dispose() { this.invalidate(); this.disposed = true; }
}

export type ContextImage = { role: string; mime: string; path: string; sha256: string; bytes: number; width: number; height: number };
export function contextImages(output: string): ContextImage[] {
  try {
    const value = JSON.parse(output);
    if (value.schema !== 'cerveau.devcheck.v1' || value.ok !== true || value.verdict !== 'captured' || !Array.isArray(value.artifacts)) return [];
    return value.artifacts.filter((item: ContextImage) => item.role === 'context-image' && item.mime === 'image/jpeg'
      && typeof item.path === 'string' && item.path.startsWith('.devcheck/') && !item.path.split('/').some(part => ['..', '.'].includes(part)) && !item.path.includes('\\')
      && /^[a-f0-9]{64}$/.test(item.sha256) && Number.isInteger(item.bytes) && item.bytes > 0 && item.bytes <= 262144
      && Number.isInteger(item.width) && Number.isInteger(item.height) && item.width > 0 && item.height > 0 && item.width <= 1280 && item.height <= 960).slice(0, 1);
  } catch { return []; }
}

export function attachContextImage(image: ContextImage, sessionId: string | null) {
  if (!sessionId) return;
  window.dispatchEvent(new CustomEvent('rfx:attach-image', { detail: { sessionId, path: image.path, sha256: image.sha256 } }));
}
