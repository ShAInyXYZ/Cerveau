// Packaging trusts the just-built executable's immutable metadata, not an
// independently parsed installed pack or a second hardcoded version string.
export const releaseArtifacts = ['crv', 'crvcli', 'build.json'];

export function plannerFromBinary(info, version, revision) {
  const p = info?.planner;
  if (info?.version !== version || info?.revision !== revision || !p ||
      p.name !== 'planner' || p.origin !== 'builtin' || p.precedence !== 'builtin-wins' ||
      !/^\d+\.\d+\.\d+$/.test(p.version ?? '') || !/^[a-f0-9]{64}$/.test(p.content_sha256 ?? '') ||
      p.error || p.loaded !== false || !Array.isArray(p.ignored_installed) || p.ignored_installed.length) {
    throw new Error('Built executable does not report the expected immutable bundled Planner identity.');
  }
  return { ...p, ignored_installed: [] };
}
