import { describe, it, expect, beforeEach } from 'vitest';
import { storage, storageKeys } from './storage';

// minimal localStorage shim for the node test environment
const mem = new Map<string, string>();
(globalThis as Record<string, unknown>).localStorage = {
  getItem: (k: string) => mem.get(k) ?? null,
  setItem: (k: string, v: string) => void mem.set(k, v),
  removeItem: (k: string) => void mem.delete(k),
};

describe('storage', () => {
  beforeEach(() => mem.clear());

  it('round-trips typed values under registered keys', () => {
    storage.set(storageKeys.dismissedErrors('s1'), ['a', 'b']);
    expect(storage.get(storageKeys.dismissedErrors('s1'), [])).toEqual(['a', 'b']);
  });

  it('returns the fallback on missing or corrupt data', () => {
    expect(storage.get(storageKeys.planArchived('nope'), false)).toBe(false);
    mem.set('crv:plan-archived:x', '{corrupt');
    expect(storage.get(storageKeys.planArchived('x'), false)).toBe(false);
  });

  it('namespaces every key under crv:', () => {
    storage.set(storageKeys.soundMuted, true);
    expect([...mem.keys()].every((k) => k.startsWith('crv:'))).toBe(true);
  });
});
