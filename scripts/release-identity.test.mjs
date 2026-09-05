import assert from 'node:assert/strict';
import { test } from 'node:test';
import { releaseArtifacts, plannerFromBinary } from './release-identity.mjs';

const identity = () => ({version:'0.6.0-alpha', revision:'test-build', planner:{
  name:'planner', version:'7.8.9', origin:'builtin', content_sha256:'a'.repeat(64),
  precedence:'builtin-wins', loaded:false, ignored_installed:[],
}});

test('release owns one executable-bundled Planner, not a second install artifact', () => {
  assert.deepEqual(releaseArtifacts, ['crv','crvcli','build.json']);
  assert.deepEqual(plannerFromBinary(identity(), '0.6.0-alpha', 'test-build'), identity().planner);
});

test('packaging refuses mismatched binary identity or non-bundled Planner', () => {
  for (const mutate of [
    i => i.version = 'old', i => i.revision = 'wrong', i => i.planner.origin = 'installed',
    i => i.planner.content_sha256 = '', i => i.planner.error = 'invalid embedded manifest',
    i => i.planner.precedence = 'installed-first', i => i.planner.version = '',
    i => i.planner.loaded = true, i => i.planner.ignored_installed = [{path:'/unexpected-runtime-read'}],
  ]) {
    const info=identity(); mutate(info);
    assert.throws(() => plannerFromBinary(info, '0.6.0-alpha', 'test-build'));
  }
});
