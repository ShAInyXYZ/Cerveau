export async function j(url, opts) {
  try {
    const r = await fetch(url, opts);
    if (!r.ok) return null;
    return await r.json();
  } catch {
    return null;
  }
}

export async function jpost(url, body) {
  return j(url, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body ?? {})
  });
}

export async function fetchEvents(url) {
  try {
    const r = await fetch(url);
    if (!r.ok) return [];
    const text = await r.text();
    return text.trim().split('\n').filter(Boolean).map((l) => JSON.parse(l));
  } catch {
    return [];
  }
}

export function relTime(ts) {
  const s = Math.max(0, (Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 60) return `${Math.floor(s)}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

export function fmtTime(ts) {
  return new Date(ts).toLocaleTimeString([], { hour12: false });
}

// Subscribe to a session's live episodic event stream (SSE). onEvent(evt) fires
// per event; returns a close() fn. Replays existing events then follows new ones.
export function streamEvents(sessionId, onEvent) {
  const es = new EventSource(`/api/sessions/${sessionId}/stream`);
  es.onmessage = (m) => {
    try { onEvent(JSON.parse(m.data)); } catch { /* ignore heartbeats/partials */ }
  };
  return () => es.close();
}
