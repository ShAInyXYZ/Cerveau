import { expect, test } from 'vitest';
import { render } from 'svelte/server';
import PackSource from './PackSource.svelte';

test('built-in pack states precedence even without an installed copy', () => {
  const { body } = render(PackSource, { props: { pack: { origin: 'builtin' } } });
  expect(body).toContain('Built into Cerveau');
  expect(body).toContain('Takes precedence over installed copies');
  expect(body).not.toContain('ignored; files unchanged');
});

test('ignored installed copies are disclosed and their paths are escaped', () => {
  const { body } = render(PackSource, { props: { pack: {
    origin: 'builtin', ignored_installed: [{ path: '/tmp/<old>/planner', version: '1.4.0' }],
  } } });
  expect(body).toContain('1 installed copy ignored; files unchanged');
  expect(body).toContain('/tmp/&lt;old>/planner');
  expect(body).not.toContain('<old>');
  expect(body).toContain('1.4.0');
});

test('external packs are not labelled as built-in', () => {
  const { body } = render(PackSource, { props: { pack: { origin: 'installed' } } });
  expect(body).not.toContain('Built into Cerveau');
  expect(body).not.toContain('Takes precedence');
});
