import { expect, test } from 'vitest';
import { matrixVersion } from './version';

test('matrix reports the actual release and prerelease', () => {
  expect(matrixVersion('0.6.0-alpha')).toBe('V0.6');
  expect(matrixVersion('0.5.0-alpha')).toBe('V0.5');
  expect(matrixVersion('v0.6.1')).toBe('V0.6');
});
test('unknown or malformed health never impersonates a release', () => {
  for (const value of [undefined, '', 'loop-v2', '0.6 garbage']) expect(matrixVersion(value)).toBe('...');
});
