// Typed localStorage registry. Every persisted key is declared HERE — no more
// ad-hoc `crv:*` strings scattered through components, no unknown persistence.

export const storageKeys = {
  /** per-session error-card dismissals (string[] of error signatures) */
  dismissedErrors: (sessionId: string) => `crv:dismissed:${sessionId}`,
  /** a finished plan the strip has celebrated and archived ('1') */
  planArchived: (planId: string) => `crv:plan-archived:${planId}`,
  /** user muted the UI sounds (boolean) */
  soundMuted: 'crv:sound-muted',
} as const;

export const storage = {
  get<T>(key: string, fallback: T): T {
    try {
      const raw = localStorage.getItem(key);
      return raw === null ? fallback : (JSON.parse(raw) as T);
    } catch {
      return fallback;
    }
  },
  set(key: string, value: unknown): void {
    try {
      localStorage.setItem(key, JSON.stringify(value));
    } catch {
      /* private mode / quota — persistence is best-effort */
    }
  },
  remove(key: string): void {
    try {
      localStorage.removeItem(key);
    } catch {
      /* ignore */
    }
  },
};
