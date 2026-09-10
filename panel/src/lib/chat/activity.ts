import { callSummary, toStep } from '../steps';
import type { EpisodicEvent, LiveStep } from '../types';

/** A presentation projection only. Original events remain attached, unchanged. */
export interface ActivityItem extends LiveStep {
  event: EpisodicEvent;
  result?: EpisodicEvent;
  args?: Record<string, unknown>;
  scope?: string;
  sourceSHA?: string;
  segment: number;
  index: number;
  resultIndex?: number;
}

export interface ReadGroup {
  kind: 'reads';
  id: string;
  path: string;
  scope: string;
  items: ActivityItem[];
}
export type ActivityRow = ActivityItem | ReadGroup;

/** Compact technical coaching only; failures remain visible and all exact text
 * stays in the disclosure. This is presentation, never a rewritten event. */
export function activityNotePreview(item: ActivityItem): string | null {
  if (item.kind === 'error' || item.kind === 'abort' || !item.text || item.text.length < 180) return null;
  if (item.text.startsWith('RECOVERY CHECKPOINT:')) return 'Recovery checkpoint · evidence retained; next repair must be targeted';
  if (item.text.startsWith('Verified copies before this attempt;')) return 'Recovery copies saved · no automatic restore';
  if (item.text.startsWith('Output limit reached before')) return 'Output limit reached · no action executed; bounded retry';
  return item.text.slice(0, 150).trimEnd() + '…';
}

const object = (v: unknown): Record<string, unknown> | undefined =>
  v !== null && typeof v === 'object' && !Array.isArray(v) ? v as Record<string, unknown> : undefined;
const string = (v: unknown): string => typeof v === 'string' ? v : '';
const positive = (v: unknown): number | undefined =>
  typeof v === 'number' && Number.isInteger(v) && v > 0 ? v : undefined;

function readScope(item: ActivityItem): string {
  if (item.name !== 'read') return '';
  // New read receipts report actual coverage, including auto-advanced pages.
  // Requested lines are only a fallback, never presented as actual coverage.
  const first = item.output?.split('\n', 1)[0] ?? '';
  if (first.startsWith('[read ') && first.endsWith(']')) {
    try {
      const meta = object(JSON.parse(first.slice(6, -1)));
      // A read can observe an external edit without any intervening tool call.
      // Keep its receipt version so grouping cannot conceal that boundary.
      item.sourceSHA = string(meta?.sha256) || undefined;
      const from = positive(meta?.from_line), to = positive(meta?.to_line);
      if (from && to && to >= from) return `lines ${from}–${to}`;
    } catch { /* Legacy or malformed output stays available in full below. */ }
  }
  const args = item.args;
  const from = positive(args?.from_line), to = positive(args?.to_line);
  if (from && to && to >= from) return `requested lines ${from}–${to}`;
  if (from) return `requested from line ${from}`;
  if (to) return `requested through line ${to}`;
  const symbol = string(args?.symbol);
  if (symbol) return `symbol ${symbol}`;
  if (typeof args?.offset === 'number' && args.offset >= 0) return `requested byte ${args.offset}`;
  return '';
}

/** Pair by recorded tool-call identity, not by the latest tool with that name. */
export function projectActivity(events: EpisodicEvent[]): ActivityItem[] {
  const items: ActivityItem[] = [];
  let segment = 0;
  let execution = '';
  let runID: unknown;
  for (const [index, event] of events.entries()) {
    const p = event.payload ?? {};
    if (p.run_id !== undefined && p.run_id !== runID) {
      segment++;
      runID = p.run_id;
    }
    if (event.type === 'run.state') {
      // model_call/tool_call alternate for ordinary reads; they are not a
      // semantic phase change. Recovery, verification and step changes are.
      const phase = ['model_call', 'tool_call'].includes(string(p.phase)) ? 'executing' : p.phase;
      const next = JSON.stringify([p.id, p.step, p.status, p.recovery_phase, phase]);
      if (next !== execution) segment++;
      execution = next;
      continue;
    }
    if (event.type === 'tool.call') {
      const args = object(p.args);
      const name = string(p.name);
      const item: ActivityItem = {
        id: event.id, kind: 'tool', name, arg: callSummary(name, args),
        status: 'run', event, args, segment, index,
      };
      item.scope = readScope(item);
      items.push(item);
      continue;
    }
    if (event.type === 'tool.result') {
      const id = string(p.id);
      const candidates = items.filter(item => item.kind === 'tool' && !item.result &&
        item.name === string(p.name) && item.event.payload?.run_id === p.run_id &&
        (id ? item.event.payload?.id === id : !item.event.payload?.id));
      // Old recordings without call IDs can pair only when unambiguous.
      // An orphan result is displayed as such, never attached to the wrong call.
      if (candidates.length === 1) {
        const item = candidates[0];
        item.result = event;
        item.resultIndex = index;
        item.status = p.ok === true ? 'ok' : p.ok === false ? 'fail' : undefined;
        item.output = string(p.output);
        item.scope = readScope(item);
      } else {
        items.push({ ...toStep(event)!, status: p.ok === true ? 'ok' : p.ok === false ? 'fail' : undefined,
          event, segment: ++segment, index });
      }
      continue;
    }
    // Tool-only assistant messages are routine model rounds, not new prose.
    if (event.type === 'msg.assistant' && !string(p.text).trim()) continue;
    // Even a textless note (e.g. verify_started) separates activities.
    segment++;
    const step = toStep(event);
    if (step) items.push({ ...step, event, segment, index });
  }
  return items;
}

function successfulRead(item: ActivityItem): boolean {
  return item.kind === 'tool' && item.name === 'read' && item.status === 'ok' && !!item.arg;
}

/** Collapse only adjacent, completed, successful reads of the exact same path. */
export function groupActivity(items: ActivityItem[]): ActivityRow[] {
  const rows: ActivityRow[] = [];
  for (const item of items) {
    const previous = rows[rows.length - 1];
    const last = previous?.kind === 'reads' ? previous.items.at(-1)! : previous;
    if (successfulRead(item) && last && successfulRead(last) && last.arg === item.arg && last.sourceSHA === item.sourceSHA &&
        last.segment === item.segment && last.resultIndex !== undefined && last.resultIndex < item.index) {
      if (previous.kind === 'reads') previous.items.push(item);
      else rows[rows.length - 1] = { kind: 'reads', id: `reads:${last.id}`, path: item.arg!, scope: '', items: [last, item] };
      const group = rows[rows.length - 1] as ReadGroup;
      const scopes = [...new Set(group.items.map(read => read.scope).filter(Boolean))];
      group.scope = scopes.slice(0, 3).join(' · ') + (scopes.length > 3 ? ' · more ranges' : '');
    } else rows.push(item);
  }
  return rows;
}
