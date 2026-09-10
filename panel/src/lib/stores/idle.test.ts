import { test, expect, vi, afterEach } from 'vitest';
const { idleStatus } = vi.hoisted(() => ({ idleStatus: vi.fn() }));
vi.mock('../api', () => ({ api: { idleStatus } }));
import { idleStore } from './idle.svelte';
const state = (s: string) => ({ state: s, park_in_seconds: s === 'warning' ? 30 : -1, enabled: true }) as any;
afterEach(() => { idleStore.stop(); vi.useRealTimers(); });

test('confirmed idle closes warning and leaves a nonblocking notice', () => {
  vi.useFakeTimers();
  idleStore.set(state('warning'));
  expect(idleStore.visible).toBe(true);
  idleStore.set(state('parked'));
  expect(idleStore.visible).toBe(false);
  expect(idleStore.notice).toContain('already idle');
  idleStore.set(state('waking'));
  expect(idleStore.visible).toBe(false);
  expect(idleStore.notice).toContain('waking');
  idleStore.set(state('active'));
  expect(idleStore.notice).toBe('');
});

test('fresh polling of an already parked Core never reopens the warning', async () => {
  vi.useFakeTimers();
  idleStore.set(state('active')); idleStore.stop();
  idleStatus.mockResolvedValue(state('parked'));
  idleStore.start();
  await vi.advanceTimersByTimeAsync(0);
  expect(idleStore.visible).toBe(false);
  expect(idleStore.notice).toContain('already idle');
  await vi.advanceTimersByTimeAsync(30_000);
  expect(idleStore.visible).toBe(false);
});

test('expired countdown or unavailable observation is not proof of idle', () => {
  vi.useFakeTimers();
  idleStore.set({ ...state('warning'), park_in_seconds: -5 });
  expect(idleStore.visible).toBe(true);
  expect(idleStore.notice).toBe('');
  idleStore.set(state('unavailable'));
  expect(idleStore.visible).toBe(false);
  expect(idleStore.notice).toBe('');
});
