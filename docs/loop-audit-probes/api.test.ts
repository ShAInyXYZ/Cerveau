// Vitest globals are enabled by this audit's isolated config.

test('L10: API preserves actionable HTTP failure instead of null', async () => {
  vi.stubGlobal('localStorage', { getItem: () => null });
  vi.stubGlobal('location', { origin: 'http://audit.invalid' });
  vi.stubGlobal('window', globalThis);
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'run already active' }), {
    status: 409, headers: { 'content-type': 'application/json' },
  })));
  const { api } = await import('../../panel/src/lib/api');
  // Either a typed error result or an exception could satisfy the future contract;
  // this probe specifically demonstrates that baseline silently returns null.
  const result = await api.rewind('A', 'event1');
  expect(result).not.toBeNull();
});
