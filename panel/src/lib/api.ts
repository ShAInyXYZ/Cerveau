// Typed API client — the single place the panel talks to the core.
// Every endpoint is a named function; components never string-build URLs.
import type {
  ChatMessage, ChatResult, EpisodicEvent, Health, Mode, PlanReport,
  Question, SessionError, SessionMeta, RunState, PlanState, SessionSnapshot,
} from './types';

// Auth — the core gates everything behind a bearer token once paired.
// The phone app pairs via /api/pair; the panel unlocks by pasting the
// token (persisted in localStorage).
const AUTH_KEY = 'crv.auth';
let authToken: string | null = null;
try { authToken = localStorage.getItem(AUTH_KEY); } catch { /* no storage */ }
export function setAuthToken(t: string | null) {
  authToken = t;
  try { t ? localStorage.setItem(AUTH_KEY, t) : localStorage.removeItem(AUTH_KEY); } catch {}
}

const authRequiredListeners = new Set<() => void>();
export function onAuthRequired(fn: () => void) { authRequiredListeners.add(fn); }
function signalAuthRequired() { authRequiredListeners.forEach((f) => f()); }

// Fetch shim: EVERY panel request (raw fetches included) carries the token;
// a 401 flips the lock screen via onAuthRequired.
const rawFetch = window.fetch.bind(window);
window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
  const u = new URL(url, location.origin);
  const isApi = u.origin === location.origin && u.pathname.startsWith('/api/');
  let merged: RequestInit = init ?? {};
  if (isApi && authToken) {
    const h = new Headers(init?.headers);
    h.set('authorization', `Bearer ${authToken}`);
    merged = { ...merged, headers: h };
  }
  const r = await rawFetch(input, merged);
  if (r.status === 401 && isApi) signalAuthRequired();
  return r;
};

async function getJSON<T>(url: string, opts?: RequestInit): Promise<T | null> {
  try {
    const r = await fetch(url, opts);
    if (!r.ok) return null;
    return (await r.json()) as T;
  } catch {
    return null;
  }
}

export class ApiError extends Error {
 constructor(public status: number, message: string) { super(message); this.name='ApiError'; }
}
export interface RunControl { run_id: string; control_id: string; control_version: number }
async function postJSON<T>(url: string, body?: unknown): Promise<T> {
 const r = await fetch(url, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body ?? {}),
 });
 const value = await r.json().catch(() => null);
 if (!r.ok) throw new ApiError(r.status, value?.error || `Request failed (HTTP ${r.status})`);
 if (value === null) throw new ApiError(r.status, 'The server returned no acknowledgement.');
 return value as T;
}

export const api = {
  command: (id: string, body: unknown) => postJSON<{run: RunState}>(`/api/sessions/${id}/commands`, body),
  resume: (id: string, control: RunControl) => postJSON<{run:RunState}>(`/api/sessions/${id}/resume`, control),
  health: () => getJSON<Health>('/api/health'),
  sessions: async (): Promise<SessionMeta[]> =>
    (await getJSON<{ sessions?: SessionMeta[] }>('/api/sessions'))?.sessions ?? [],
  /** ids of sessions with a turn executing right now — includes CLI runs the
   *  panel did not start, which are otherwise invisible here. */
  runningSessions: async (): Promise<string[]> =>
    (await getJSON<{ running?: string[] }>('/api/sessions'))?.running ?? [],
  sessionState: (id: string) =>
    getJSON<SessionSnapshot>(`/api/sessions/${id}/state`),
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

  chat: (id: string, text: string, mode: Mode, step: boolean, sampling?: string) =>
    postJSON<ChatResult>(`/api/sessions/${id}/chat`, { text, mode, step, sampling }),
  getSampling: () => getJSON<{ active: string; presets: string[] }>('/api/sampling'),
  /** drop an event and everything after it — how "edit a message" works on an
   *  append-only log */
  rewind: (id: string, eventId: string) =>
    postJSON<{ ok: boolean }>(`/api/sessions/${id}/rewind`, { event_id: eventId }),
  steer: (id: string, text: string, control: RunControl) => postJSON<{run:RunState}>(`/api/sessions/${id}/steer`, { ...control, text }),
  pause: (id: string, control: RunControl) => postJSON<{run:RunState}>(`/api/sessions/${id}/pause`, control),
  kill: (id: string, control: RunControl) => postJSON<{run:RunState}>(`/api/sessions/${id}/kill`, control),
  answer: (id: string, answer: string, question_id?: string, run_id?: string) => postJSON(`/api/sessions/${id}/answer`, { answer, question_id, run_id }),
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

  // ── Idle / power parking ──
  idleStatus: () => getJSON<IdleStatus>('/api/idle'),
  idleStay: (minutes?: number) =>
    postJSON<IdleStatus>('/api/idle/stay', { minutes: minutes ?? 0 }),
  idleNow: () => postJSON<IdleStatus>('/api/idle/now', {}),
  idleConfig: (c: { after_minutes?: number; warn_minutes?: number; enabled?: boolean }) =>
    postJSON<IdleStatus>('/api/idle/config', c),
};

export type IdleStatus = {
  state: 'active' | 'warning' | 'parked' | 'waking';
  idle_seconds: number;
  park_in_seconds: number;
  enabled: boolean;
  held: boolean;
  after_seconds: number;
  warn_seconds: number;
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
  after = '',
  onConnect?: () => void,
): () => void {
  let es: EventSource | null = null;
  let closed = false;
  let retryMs = 1000;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const connect = () => {
    if (closed) return;
    es = new EventSource(`/api/sessions/${sessionId}/stream?after=${encodeURIComponent(after)}`);
    es.onopen = () => { retryMs = 1000; onConnect?.(); };
    es.onmessage = (m) => {
      try {
        const event = JSON.parse(m.data) as EpisodicEvent;
        if (after && Number(event.id.replace('evt_', '')) <= Number(after.replace('evt_', ''))) return;
        after = event.id;
        onEvent(event);
      } catch { /* heartbeats/partials */ }
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
export const jput = <T,>(url: string, body?: unknown) => getJSON<T>(url, {
  method: 'PUT',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify(body ?? {}),
});
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
