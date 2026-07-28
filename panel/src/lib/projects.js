// Group flat sessions into projects. A project = the workspace folder a session
// was created in. Derived, not stored: we bucket by the `workspace` field.

export function groupByProject(sessions) {
  const map = new Map();
  const instant = [];
  for (const s of sessions) {
    if (s.instant) { instant.push(s); continue; } // ephemeral — its own group
    const ws = s.workspace || '(no workspace)';
    if (!map.has(ws)) map.set(ws, []);
    map.get(ws).push(s);
  }
  const projects = [];
  for (const [path, sess] of map) {
    projects.push({ path, name: projectName(path), sessions: sess });
  }
  // most-recently-active project first
  projects.sort((a, b) => lastTs(b.sessions) - lastTs(a.sessions));
  // the Instant group pins to the TOP (it's transient, users want it handy)
  if (instant.length) {
    instant.sort((a, b) => lastTs([b]) - lastTs([a]));
    projects.unshift({ path: '__instant__', name: 'Instant', instant: true, sessions: instant });
  }
  return projects;
}

// short display name = last path segment
export function projectName(path) {
  if (!path || path === '(no workspace)') return 'unassigned';
  const clean = path.replace(/\/+$/, '');
  const seg = clean.split(/[/\\]/).pop();
  return seg || clean;
}

function lastTs(sessions) {
  return sessions.reduce((max, s) => {
    const t = s.created ? new Date(s.created).getTime() : 0;
    return t > max ? t : max;
  }, 0);
}
