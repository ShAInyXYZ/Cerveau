/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { createContext, runInContext } from 'node:vm';
import { expect, test, vi } from 'vitest';

// Exercise the actual bridge and bundled panel script without a DOM renderer.
// Network and the small DOM surface used by Planner are explicit test doubles.
const bridgeScript = readFileSync(new URL('./RfxCustomPanel.svelte', import.meta.url), 'utf8')
  .match(/<script>([\s\S]*?)<\/script>/)![1].replace(/^\s*import .*?;\s*$/gm, '');
const plannerScript = readFileSync(new URL('../../../rfx/planner/ui/panel.html', import.meta.url), 'utf8')
  .match(/<script>([\s\S]*?)<\/script>/)![1];

function hostFixture() {
  const source = { postMessage: vi.fn() };
  const j = vi.fn();
  const jpost = vi.fn();
  let sequence = 0;
  const randomUUID = vi.fn(() => `command-${++sequence}`);
  const context = createContext({
    j, jpost, source, ApiError: class ApiError extends Error {},
    sessionStore: { activeId: 's' }, crypto: { randomUUID },
    $props: () => ({ pack: { ui: { session: true, turn: true } }, members: [], sessionId: 's' }),
    $state: (v: unknown) => v, $derived: (v: unknown) => v, $effect: () => {},
    rfxIcon: () => null, Zap: null, setTimeout,
  });
  runInContext(bridgeScript, context);
  return { context, source, j, jpost, randomUUID };
}

test('RFX failed plan GET reports failure rather than ok:true', async () => {
  const f = hostFixture();
  f.j.mockResolvedValue(null);
  await runInContext('readPlan(1, source)', f.context);
  expect(f.source.postMessage.mock.calls[0][0]).toMatchObject({ ok: false });
  expect(f.source.postMessage.mock.calls[0][0].error).toContain('Could not load');
});

test('RFX selection is one plan-bound command, not a loop of step requests', async () => {
  const f = hostFixture();
  f.j.mockResolvedValueOnce({ plan_event_id: 'plan-1' })
    .mockResolvedValueOnce({ running: false, run: { id: 'run-1', status: 'completed' } });
  f.jpost.mockResolvedValue({ run: { id: 'run-1' } });
  await runInContext("runStep(1, 'selected', false, source, [0, 1], 'plan-1')", f.context);
  expect(f.jpost).toHaveBeenCalledTimes(1);
  expect(f.jpost.mock.calls[0]).toEqual(['/api/sessions/s/commands', {
    command_id: 'command-1', kind: 'selected', step: -1, steps: [0, 1], revision: false, plan_event_id: 'plan-1',
  }]);
  expect(f.source.postMessage.mock.calls[0][0]).toMatchObject({ ok: true });
});

test('RFX stale displayed plan cannot start work on the replacement plan', async () => {
  const f = hostFixture();
  f.j.mockResolvedValue({ plan_event_id: 'replacement' });
  await runInContext("runStep(1, 'selected', false, source, [0, 1], 'old-plan')", f.context);
  expect(f.jpost).not.toHaveBeenCalled();
  expect(f.source.postMessage.mock.calls[0][0]).toMatchObject({ ok: false });
});

test('RFX uncertain POST retry reuses the original command identity', async () => {
  const f = hostFixture();
  f.j.mockResolvedValueOnce({ plan_event_id: 'plan-1' })
    .mockResolvedValueOnce({ plan_event_id: 'plan-1' })
    .mockResolvedValueOnce({ running: false, run: { id: 'run-1', status: 'completed' } });
  f.jpost.mockRejectedValueOnce(new TypeError('connection lost'))
    .mockResolvedValueOnce({ run: { id: 'run-1' } });
  await runInContext("runStep(1, 'selected', false, source, [0], 'plan-1')", f.context);
  await runInContext("runStep(2, 'selected', false, source, [0], 'plan-1')", f.context);
  expect(f.jpost).toHaveBeenCalledTimes(2);
  expect(f.jpost.mock.calls[0][1].command_id).toBe(f.jpost.mock.calls[1][1].command_id);
  expect(f.randomUUID).toHaveBeenCalledTimes(1);
});

test('RFX session switch before plan GET resolves cannot start the stale request', async () => {
  const f = hostFixture();
  let finish: (value: unknown) => void = () => {};
  f.j.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  const task = runInContext("runStep(1, 'selected', false, source, [0], 'plan-1')", f.context);
  runInContext("sessionId = 'other'", f.context);
  finish({ plan_event_id: 'plan-1' });
  await task;
  expect(f.jpost).not.toHaveBeenCalled();
  expect(f.source.postMessage.mock.calls[0][0]).toMatchObject({ ok: false });
});

test('Planner polling is read-only; selection uses the single server scope bridge', async () => {
  const plan = { title: 'fixture', steps: [{ title: 'one', files: [] }, { title: 'two', files: [] }] };
  const state = { ok: true, running: false, plan,
    planState: { plan_event_id: 'plan-1', next: 0, steps: [{ status: 'pending' }, { status: 'pending' }] },
    run: { status: 'interrupted' } };
  const rfx = { session: vi.fn().mockResolvedValue(state), fit: vi.fn(),
    runStep: vi.fn(), runPlan: vi.fn(), runSelected: vi.fn().mockResolvedValue({ ok: true }) };
  const app = { innerHTML: '', querySelectorAll: () => [] };
  const context = createContext({ rfx, document: { getElementById: () => app }, setInterval: vi.fn() });
  runInContext(plannerScript, context);
  await runInContext('refresh()', context);
  await runInContext('refresh()', context);
  expect(rfx.runStep).not.toHaveBeenCalled();
  expect(rfx.runPlan).not.toHaveBeenCalled();
  expect(rfx.runSelected).not.toHaveBeenCalled();
  await runInContext("selected.add(1); selected.add(0); kick('selected')", context);
  expect(rfx.runSelected).toHaveBeenCalledExactlyOnceWith([0, 1], 'plan-1');
  expect(rfx.runStep).not.toHaveBeenCalled();
});
