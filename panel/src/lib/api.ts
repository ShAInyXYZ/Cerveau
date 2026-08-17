// Typed API client — the single place the panel talks to the core.
// Every endpoint is a named function; components never string-build URLs.
import type {
  ChatMessage, ChatResult, EpisodicEvent, Health, Mode, PlanReport,
  Question, SessionError, SessionMeta,
} from './types';

async function getJSON<T>(url: string, opts?: RequestInit): Promise<T | null> {
  try {
    const r = await fetch(url, opts);
    if (!r.ok) return null;
    return (await r.json()) as T;
  } catch {
    return null;
  }
}

function postJSON<T>(url: string, body?: unknown): Promise<T | null> {
  return getJSON<T>(url, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body ?? {}),
  });
}

export const api = {
  health: () => getJSON<Health>('/api/health'),
  sessions: async (): Promise<SessionMeta[]> =>
    (await getJSON<{ sessions?: SessionMeta[] }>('/api/sessions'))?.sessions ?? [],
  sessionState: (id: string) =>
    getJSON<{ messages?: ChatMessage[] }>(`/api/sessions/${id}/state`),
  events: async (id: string): Promise<EpisodicEvent[]> => {
    try {
      const r = await fetch(`/api/sessions/${id}/events`);
      if (!r.ok) return [];
      const text = await r.text();
      return text.trim().split('\n').filter(Boolean).map((l) => JSON.parse(l));
    } catch {
      return [];
    }
  },
  question: (id: string) => getJSON<Question | null>(`/api/sessions/${id}/question`),
  errors: async (id: string): Promise<SessionError[]> =>
    (await getJSON<{ errors?: SessionError[] }>(`/api/sessions/${id}/errors`))?.errors ?? [],
  report: (id: string) => getJSON<PlanReport>(`/api/sessions/${id}/report`),
  skills: async (): Promise<unknown[]> =>
    (await getJSON<{ skills?: unknown[] }>('/api/skills'))?.skills ?? [],
  deletePreview: (id: string) => getJSON<unknown>(`/api/sessions/${id}/delete-preview`),

  chat: (id: string, text: string, mode: Mode, step: boolean) =>
    postJSON<ChatResult>(`/api/sessions/${id}/chat`, { text, mode, step }),
  steer: (id: string, text: string) => postJSON(`/api/sessions/${id}/steer`, { text }),
  pause: (id: string) => postJSON(`/api/sessions/${id}/pause`),
  kill: (id: string) => postJSON(`/api/sessions/${id}/kill`),
  answer: (id: string, answer: string) => postJSON(`/api/sessions/${id}/answer`, { answer }),
  autopilot: (id: string) => postJSON(`/api/sessions/${id}/autopilot`),

  createSession: (name: string, workspace?: string) =>
    postJSON<SessionMeta>('/api/sessions', { name, workspace }),
  createInstant: () => postJSON<SessionMeta>('/api/sessions/instant', {}),
  renameSession: (id: string, name: string) =>
    fetch(`/api/sessions/${id}`, {
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ name }),
    }).then((r) => r.ok),
  deleteSession: (id: string, mode: string, confirm: string) =>
    fetch(`/api/sessions/${id}`, {
      method: 'DELETE',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ mode, confirm }),
    }).then((r) => r.ok),
  setWorkspace: (path: string) =>
    postJSON<{ ok?: string }>('/api/config/workspace', { path }),
};

/**
 * Subscribe to a session's live event stream. Unlike a bare EventSource, this
 * reconnects with capped exponential backoff when the core restarts — the
 * stream dying silently used to leave the panel blind until a reload.
 * Returns a close() that also stops reconnecting.
 */
export function streamEvents(
  sessionId: string,
  onEvent: (ev: EpisodicEvent) => void,
): () => void {
  let es: EventSource | null = null;
  let closed = false;
  let retryMs = 1000;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const connect = () => {
    if (closed) return;
    es = new EventSource(`/api/sessions/${sessionId}/stream`);
    es.onopen = () => { retryMs = 1000; };
    es.onmessage = (m) => {
      try { onEvent(JSON.parse(m.data)); } catch { /* heartbeats/partials */ }
    };
    es.onerror = () => {
      es?.close();
      if (closed) return;
      timer = setTimeout(connect, retryMs);
      retryMs = Math.min(retryMs * 2, 15_000);
    };
  };
  connect();

  return () => {
    closed = true;
    if (timer) clearTimeout(timer);
    es?.close();
  };
}

// ---- formatting helpers (pure) ----

export function relTime(ts: string | number): string {
  const s = Math.max(0, (Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 60) return `${Math.floor(s)}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

export function fmtTime(ts: string | number): string {
  return new Date(ts).toLocaleTimeString([], { hour12: false });
}

// ---- legacy compat (JS components not yet migrated) ----
// Prefer `api.*` in new code; these mirror the old api.js surface.
export const j = getJSON;
export const jpost = postJSON;
export async function fetchEvents(url: string): Promise<unknown[]> {
  try {
    const r = await fetch(url);
    if (!r.ok) return [];
    const text = await r.text();
    return text.trim().split('\n').filter(Boolean).map((l) => JSON.parse(l));
  } catch {
    return [];
  }
}
