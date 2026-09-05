// Read the running harness version, never invent a release while disconnected.
export function matrixVersion(version?: string): string {
  const match = version?.match(/^v?(\d+)\.(\d+)(?:\.\d+)?(-[\w.-]+)?$/);
  if (!match) return '...';
  // Version only. The pre-release tag lives in /api/build; the matrix is a
  // badge, not a changelog, and "ALPHA" doubled its width (2026-09-05).
  return `V${match[1]}.${match[2]}`;
}
