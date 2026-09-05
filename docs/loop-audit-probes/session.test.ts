// Actual Svelte store, compiled by the existing Vite plugin. Network and audio mocked.
// Vitest globals avoid depending on a node_modules link outside panel/.

const { api, play } = vi.hoisted(() => ({
  api: Object.fromEntries(['sessionState','events','errors','report','question','setWorkspace',
    'sessions','runningSessions','skills','chat','rewind'].map(k => [k, vi.fn()])),
  play: vi.fn(),
}));
vi.mock('../../panel/src/lib/api', () => ({ api, streamEvents: () => () => {} }));
vi.mock('../../panel/src/lib/sound.js', () => ({ play }));
vi.mock('../../panel/src/lib/storage', () => ({
  storage: { get: (_k: string, fallback: unknown) => fallback, set: vi.fn() },
  storageKeys: { dismissedErrors: (id: string) => id },
}));
vi.mock('../../panel/src/lib/stores/health.svelte.ts', () => ({
  healthStore: { workspace: '', refresh: vi.fn() },
}));

const settle = () => new Promise(resolve => setImmediate(resolve));
beforeEach(() => {
  vi.resetModules(); vi.resetAllMocks(); vi.useRealTimers();
  vi.stubGlobal('localStorage', { getItem: () => null, setItem: vi.fn() });
  api.sessionState.mockResolvedValue({ messages: [] });
  for (const k of ['events','errors','sessions','runningSessions','skills']) api[k].mockResolvedValue([]);
  for (const k of ['report','question','setWorkspace','chat','rewind']) api[k].mockResolvedValue(null);
});

test('L09: a late response from session A cannot replace session B', async () => {
  const { sessionStore: s } = await import('../../panel/src/lib/stores/session.svelte.ts');
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
  const { sessionStore: s } = await import('../../panel/src/lib/stores/session.svelte.ts');
  s.select('A'); await settle();
  await s.send('hello');
  expect(play).not.toHaveBeenCalledWith('done');
});

test('L10: failed rewind cannot send replacement text', async () => {
  const { sessionStore: s } = await import('../../panel/src/lib/stores/session.svelte.ts');
  s.select('A'); await settle();
  await s.editAndResend('old-message', 'replacement');
  expect(api.chat).not.toHaveBeenCalled();
});

test('L09: server-owned run prevents another local start', async () => {
  const { sessionStore: s } = await import('../../panel/src/lib/stores/session.svelte.ts');
  api.runningSessions.mockResolvedValue(['A']);
  await s.loadSessions(); s.select('A'); await settle();
  expect(s.running).toBe(true);
  await s.send('overlapping run');
  expect(api.chat).not.toHaveBeenCalled();
});

test('L08: externally started run polls its first plan and final messages', async () => {
  const { sessionStore: s } = await import('../../panel/src/lib/stores/session.svelte.ts');
  let poll: () => void = () => {};
  vi.spyOn(globalThis, 'setInterval').mockImplementation(((fn: () => void) => { poll = fn; return 123; }) as any);
  api.runningSessions.mockResolvedValue(['A']);
  s.select('A'); s.start(); await settle();
  api.report.mockClear(); api.sessionState.mockClear();
  poll(); await settle();
  s.stop();
  expect({ reports: api.report.mock.calls.length, states: api.sessionState.mock.calls.length })
    .toEqual({ reports: 1, states: 1 });
});
