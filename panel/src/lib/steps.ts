// Pure derivations from episodic events to UI shapes — no state, fully testable.
import type { EpisodicEvent, LiveStep } from './types';

/** The human-meaningful argument of a tool call — WHAT the agent is doing. */
export function callSummary(name: string, args: Record<string, unknown> | undefined): string {
  const a = args ?? {};
  const s = (k: string) => (typeof a[k] === 'string' ? (a[k] as string) : '');
  switch (name) {
    case 'bash': return s('command');
    case 'read':
    case 'write':
    case 'edit':
    case 'outline_file': return s('path');
    case 'grep': return s('pattern') || s('query');
    case 'find_symbol':
    case 'find_references': return s('name');
    case 'web_fetch': return s('url');
    case 'check_page': return s('url') || s('path');
    case 'serve': return s('dir') || s('action');
    case 'remember': return s('content');
    case 'ask_user': return s('question');
    default: {
      const v = Object.values(a).find((x) => typeof x === 'string');
      return (v as string) ?? '';
    }
  }
}

/** Turn a raw episodic event into a live working-block step (or null). */
export function toStep(ev: EpisodicEvent): LiveStep | null {
  const p = (ev.payload ?? {}) as Record<string, unknown>;
  switch (ev.type) {
    case 'tool.call':
      return {
        id: ev.id, kind: 'tool', name: String(p.name ?? ''),
        arg: callSummary(String(p.name ?? ''), p.args as Record<string, unknown>),
        status: 'run',
      };
    case 'tool.result':
      return {
        id: ev.id, kind: 'result', name: String(p.name ?? ''),
        status: p.ok !== false ? 'ok' : 'fail',
        output: typeof p.output === 'string' ? p.output : '',
      };
    case 'note':
      return p.text ? { id: ev.id, kind: 'note', text: String(p.text) } : null;
    case 'error':
      return { id: ev.id, kind: 'error', text: String(p.what ?? p.detail ?? 'error') };
    case 'aborted':
      return { id: ev.id, kind: 'abort', text: String(p.reason ?? 'aborted') };
    default:
      return null;
  }
}

/**
 * Merge tool.call + its tool.result into one card (running → done/fail), so
 * the working block reads like a tool log: command + its outcome inline.
 */
export function mergeSteps(steps: LiveStep[]): LiveStep[] {
  const out: LiveStep[] = [];
  for (const s of steps) {
    if (s.kind === 'result') {
      const call = [...out].reverse().find(
        (c) => c.kind === 'tool' && c.name === s.name && c.status === 'run',
      );
      if (call) {
        call.status = s.status;
        call.output = s.output ?? '';
        continue;
      }
    }
    out.push({ ...s });
  }
  return out;
}

/** Stable signature for an error card, used for per-session dismissal. */
export function errorKey(
  e: { class?: string; what?: string; stop?: string; why?: string; detail?: string },
  i: number,
): string {
  return `${e.class ?? ''}|${e.what ?? e.stop ?? ''}|${e.why ?? e.detail ?? ''}|${i}`;
}
