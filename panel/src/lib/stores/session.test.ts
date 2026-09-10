import { vi, test, expect, beforeEach } from 'vitest';
// Actual Svelte store, compiled by the existing Vite plugin. Network and audio mocked.
// Vitest globals avoid depending on a node_modules link outside panel/.

const { api, play } = vi.hoisted(() => ({
  api: Object.fromEntries(['sessionState','events','errors','report','question','setWorkspace',
    'sessions','runningSessions','skills','chat','rewind','command','pause','resume','kill','steer','answer'].map(k => [k, vi.fn()])),
  play: vi.fn(),
}));
vi.mock('../api', () => ({ api, ApiError:class ApiError extends Error{}, streamEvents: () => () => {} }));
vi.mock('../sound.js', () => ({ play }));
vi.mock('../storage', () => ({
  storage: { get: (_k: string, fallback: unknown) => fallback, set: vi.fn() },
  storageKeys: { dismissedErrors: (id: string) => id },
}));
vi.mock('./health.svelte.ts', () => ({
  healthStore: { workspace: '', refresh: vi.fn() },
}));

const settle = () => new Promise(resolve => setTimeout(resolve,0));

test('plan retry explicitly requests recovery and continuation, unlike single step', async () => {
  const { sessionStore: s } = await import('./session.svelte.ts');
  api.sessionState.mockResolvedValue({ run: { id: 'old', status: 'suspended' }, plan_state: { done: false, blocked: 2, plan_event_id: 'p1' }, running: false });
  api.command.mockResolvedValue({ run: { id: 'new', status: 'completed' } });
  s.select('A'); await settle();
  await s.retry('old task');
  expect(api.command).toHaveBeenLastCalledWith('A', expect.objectContaining({ kind: 'step', step: 2, plan_event_id: 'p1', continue_plan: true }));
  await s.runStep(2);
  expect(api.command).toHaveBeenLastCalledWith('A', expect.not.objectContaining({ continue_plan: true }));
});

test('image context is preserved by edit-resend and message retry', async () => {
  const { sessionStore: s } = await import('./session.svelte.ts');
  const images = [{ data_url: 'data:image/png;base64,fixture' }];
  api.sessionState.mockResolvedValue({ messages: [{ id: 'visual', type: 'msg.user', payload: { text: 'look', images } }], running: false });
  api.rewind.mockResolvedValue({ ok: true }); api.command.mockResolvedValue({ run: { id: 'new', status: 'completed' } });
  s.select('A'); await settle();
  await s.editAndResend('visual', 'look closer');
  expect(api.command).toHaveBeenLastCalledWith('A', expect.objectContaining({ text: 'look closer', images }));
  await s.retry('look again');
  expect(api.command).toHaveBeenLastCalledWith('A', expect.objectContaining({ text: 'look again', images }));
});

test('a manual Reflex incident cannot rerun a completed plan or chat prompt', async () => {
  const { sessionStore: s } = await import('./session.svelte.ts');
  api.sessionState.mockResolvedValue({ run: { id: 'rfx', kind: 'reflex', status: 'failed' }, plan_state: { done: true }, running: false });
  s.select('A'); await settle();
  expect(await s.retry('old task')).toBe(false);
  expect(api.command).not.toHaveBeenCalled();
});
beforeEach(() => {
  vi.resetModules(); vi.resetAllMocks(); vi.useRealTimers();
  vi.stubGlobal('localStorage', { getItem: () => null, setItem: vi.fn() });
  api.sessionState.mockResolvedValue({ messages: [], running:false });
  for (const k of ['events','errors','sessions','runningSessions','skills']) api[k].mockResolvedValue([]);
  for (const k of ['report','question','setWorkspace','chat']) api[k].mockResolvedValue(null);
 api.rewind.mockRejectedValue(new Error('run active')); api.command.mockRejectedValue(new Error('connection lost'));
});

test('L09: a late response from session A cannot replace session B', async () => {
  const { sessionStore: s } = await import('./session.svelte.ts');
  let finishA: (v: any) => void = () => {};
  api.sessionState.mockImplementation((id: string) => id === 'A'
    ? new Promise(resolve => { finishA = resolve; })
    : Promise.resolve({ messages: [{ id: 'B-message', type: 'msg.user', payload: { text: 'B' } }] }));
  s.select('A'); s.select('B'); await settle();
  finishA({ messages: [{ id: 'A-message', type: 'msg.user', payload: { text: 'A' } }] });
  await settle();
  expect(s.activeId).toBe('B');
  expect(s.messages[0].id).toBe('B-message');
});

test('L10: null/failed chat response cannot announce success', async () => {
  const { sessionStore: s } = await import('../stores/session.svelte.ts');
  s.select('A'); await settle();
  await s.send('hello');
  expect(play).not.toHaveBeenCalledWith('done');
});

test('L10: failed rewind cannot send replacement text', async () => {
  const { sessionStore: s } = await import('../stores/session.svelte.ts');
  s.select('A'); await settle();
  await s.editAndResend('old-message', 'replacement');
  expect(api.command).not.toHaveBeenCalled();
});

test('L09: server-owned run prevents another local start', async () => {
  const { sessionStore: s } = await import('../stores/session.svelte.ts');
  api.runningSessions.mockResolvedValue(['A']);
 api.sessionState.mockResolvedValue({messages:[],running:true,run:{id:'external',status:'running'}});
  await s.loadSessions(); s.select('A'); await settle();
  expect(s.running).toBe(true);
  await s.send('overlapping run');
  expect(api.command).not.toHaveBeenCalled();
});

test('L08: externally started run polls its first plan and final messages', async () => {
  const { sessionStore: s } = await import('../stores/session.svelte.ts');
  let poll: () => void = () => {};
  vi.spyOn(globalThis, 'setInterval').mockImplementation(((fn: () => void) => { poll = fn; return 123; }) as any);
  api.runningSessions.mockResolvedValue(['A']);
  s.select('A'); s.start(); await settle();
  api.report.mockClear(); api.sessionState.mockClear();
  poll(); await settle();
  s.stop();
  expect({ reports: api.report.mock.calls.length, states: api.sessionState.mock.calls.length })
    .toEqual({ reports: 0, states: 1 });
});

test('failed snapshot preserves confirmed plan/question/errors and reports unknown connection',async()=>{
 const { sessionStore:s }=await import('./session.svelte.ts');
 api.sessionState.mockResolvedValueOnce({messages:[],run:{id:'r1',status:'running'},question:{id:'q1',run_id:'r1',question:'Continue?'},report:{title:'Plan',steps:[]},errors:[{id:'e1',what:'Failure'}]});
 let poll:()=>void=()=>{};
 vi.spyOn(globalThis,'setInterval').mockImplementation(((fn:()=>void)=>{poll=fn;return 123;}) as any);
 s.select('A');s.start();await settle();
 api.sessionState.mockResolvedValue(null);poll();await settle();s.stop();
 expect(s.connectionLost).toBe(true);expect(s.run?.id).toBe('r1');
 expect(s.question?.id).toBe('q1');expect(s.report?.title).toBe('Plan');expect(s.errors[0].id).toBe('e1');
});

test('control carries exact run version and uncertain retry keeps its identity',async()=>{
 const { sessionStore:s }=await import('./session.svelte.ts');
 api.sessionState.mockResolvedValue({messages:[],running:true,run:{id:'r1',status:'running',control_version:3}});
 s.select('A');await settle();
 api.pause.mockRejectedValue(new Error('lost acknowledgement'));
 await s.pause();await s.pause();
 const first=api.pause.mock.calls[0][1],second=api.pause.mock.calls[1][1];
 expect(first).toMatchObject({run_id:'r1',control_version:3});expect(first.control_id).toBeTruthy();expect(second).toEqual(first);
});

const incident = (runID='r1', id='e1') => ({
  messages: [], running: false, run: { id: runID, status: 'failed' },
  errors: [{ id, run_id: runID, class: 'failed', what: 'Tool failed' }],
});
async function observeSession() {
  const { sessionStore: s } = await import('./session.svelte.ts');
  let poll = () => {};
  vi.spyOn(globalThis, 'setInterval').mockImplementation(((fn: () => void) => { poll = fn; return 123; }) as any);
  s.select('A'); s.start(); await settle();
  return { s, async refresh(state: unknown) { api.sessionState.mockResolvedValue(state); poll(); await settle(); } };
}

test('incident chime sounds once for a new event, never repeated snapshots or reconnect', async () => {
  const { s, refresh } = await observeSession();
  await refresh(incident());
  expect(play.mock.calls).toEqual([['error']]);
  await refresh(incident());
  await refresh(null);
  await refresh(incident());
  expect(play.mock.calls).toEqual([['error']]);
  await refresh(incident('r1', 'e2'));
  expect(play.mock.calls).toEqual([['error'], ['error']]);
  s.stop();
});

test('incident chime silently hydrates history on initial load and module reload', async () => {
  api.sessionState.mockResolvedValue(incident());
  const { s } = await observeSession();
  expect(play).not.toHaveBeenCalled();
  s.stop(); vi.resetModules();
  const { s: reloaded, refresh } = await observeSession();
  expect(play).not.toHaveBeenCalled();
  await refresh(incident('r1', 'e2'));
  expect(play.mock.calls).toEqual([['error']]);
  reloaded.stop();
});

test('incident chime stays silent on session switches but scopes new IDs to the selected session', async () => {
  const { s, refresh } = await observeSession();
  await refresh(incident());
  api.sessionState.mockResolvedValue(incident());
  s.select('B'); await settle();
  expect(play.mock.calls).toEqual([['error']]);
  await refresh(incident('r1', 'e2'));
  api.sessionState.mockResolvedValue(incident());
  s.select('A'); await settle();
  expect(play.mock.calls).toEqual([['error'], ['error']]);
  await refresh(incident('r1', 'e2'));
  expect(play.mock.calls).toEqual([['error'], ['error'], ['error']]);
  s.stop();
});

test('dismissal cannot hide or silence an identical incident in a later run', async () => {
  const { s, refresh } = await observeSession();
  await refresh(incident());
  await s.dismissAllErrors();
  await refresh(incident());
  expect(s.errors).toEqual([]);
  expect(play.mock.calls).toEqual([['error']]);
  await refresh(incident('r2'));
  expect(s.errors).toHaveLength(1);
  expect(play.mock.calls).toEqual([['error'], ['error']]);
  s.stop();
});

test('a late snapshot from another session cannot sound an incident', async () => {
  const { s, refresh } = await observeSession();
  let finish: (value: unknown) => void = () => {};
  api.sessionState.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
  s.select('old'); s.select('current'); await settle();
  finish(incident()); await settle();
  expect(play).not.toHaveBeenCalled();
  await refresh(incident());
  expect(play.mock.calls).toEqual([['error']]);
  s.stop();
});
