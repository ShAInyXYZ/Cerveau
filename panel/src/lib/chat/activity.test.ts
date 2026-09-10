import { describe, expect, it } from 'vitest';
import type { EpisodicEvent } from '../types';
import { activityNotePreview, groupActivity, projectActivity } from './activity';

const event = (id: string, type: string, payload: Record<string, unknown>): EpisodicEvent =>
  ({ id, type, ts: `2026-09-06T07:00:${id.padStart(2, '0')}Z`, payload: { run_id: 'run1', ...payload } });
const call = (id: string, args: Record<string, unknown> = { path: 'world.js' }, name = 'read') =>
  event(id, 'tool.call', { id: `call${id}`, name, args });
const result = (id: string, callID: string, ok = true, output = 'source', name = 'read') =>
  event(id, 'tool.result', { id: `call${callID}`, name, ok, output });
const reads = () => [call('1'), result('2', '1'), call('3'), result('4', '3')];

describe('technical note disclosure', () => {
  it('summarizes long coaching without changing the recorded evidence', () => {
    const text = 'RECOVERY CHECKPOINT: ' + 'Retained exact diagnostic evidence. '.repeat(12);
    const recorded = event('1', 'note', {kind:'checkpoint', text});
    const item = projectActivity([recorded])[0];
    expect(activityNotePreview(item)).toBe('Recovery checkpoint · evidence retained; next repair must be targeted');
    expect(item.text).toBe(text);
    expect(recorded.payload?.text).toBe(text);
  });
  it('never folds an error or short actionable notice', () => {
    const error = projectActivity([event('1', 'error', {what:'Failure: ' + 'x'.repeat(250)})])[0];
    expect(activityNotePreview(error)).toBeNull();
    const note = projectActivity([event('2', 'note', {kind:'checkpoint', text:'Repair check 1/3 still fails.'})])[0];
    expect(activityNotePreview(note)).toBeNull();
  });
});

describe('activity grouping', () => {
  it('groups completed reads without altering or dropping original events', () => {
    const events = reads();
    const before = JSON.stringify(events);
    const items = projectActivity(events), rows = groupActivity(items);
    expect(rows).toHaveLength(1);
    expect(rows[0].kind).toBe('reads');
    if (rows[0].kind !== 'reads') throw new Error('expected a read group');
    expect(rows[0].path).toBe('world.js');
    expect(rows[0].items.map(item => item.id)).toEqual(['1', '3']);
    expect(rows[0].items[0].event).toBe(events[0]);
    expect(rows[0].items[0].result).toBe(events[1]);
    expect(rows[0].items[0].args).toBe(events[0].payload!.args);
    expect(rows[0].items[1].result?.ts).toBe(events[3].ts);
    expect(JSON.stringify(events)).toBe(before);
    expect(groupActivity(items)).toEqual(rows);
  });

  it.each([
    ['note', event('5', 'note', { kind: 'checkpoint', text: 'Repair checkpoint' })],
    ['textless verification note', event('5', 'note', { kind: 'verify_started' })],
    ['edit', call('5', { path: 'world.js' }, 'edit')],
    ['failure', event('5', 'error', { what: 'check failed' })],
    ['prose', event('5', 'msg.assistant', { text: 'Now verify the change.' })],
  ])('does not group across %s', (_, boundary) => {
    const [a, b, c, d] = reads();
    expect(groupActivity(projectActivity([a, b, boundary, c, d])).some(row => row.kind === 'reads')).toBe(false);
  });

  it('keeps pending and failed reads visible outside completed groups', () => {
    const rows = groupActivity(projectActivity([...reads(), call('5'), result('6', '5', false), call('7')]));
    expect(rows.map(row => row.kind === 'reads' ? 'group' : row.status)).toEqual(['group', 'fail', 'run']);
  });

  it('does not group different paths or results without confirmed success', () => {
    const events = [...reads(), call('5', { path: 'src/world.js' }), result('6', '5'), call('7')];
    events.push(event('8', 'tool.result', { id: 'call7', name: 'read', output: 'no status' }));
    const rows = groupActivity(projectActivity(events));
    expect(rows).toHaveLength(3);
    expect(rows[2].kind !== 'reads' && rows[2].status).toBeUndefined();
  });

  it('shows actual auto-advanced line coverage instead of treating reads as duplicates', () => {
    const events = reads();
    events[1].payload!.output = '[read {"from_line":155,"to_line":165}]\nsource';
    events[3].payload!.output = '[read {"from_line":166,"to_line":180}]\nnext page';
    const rows = groupActivity(projectActivity(events));
    expect(rows[0].kind === 'reads' && rows[0].scope).toBe('lines 155–165 · lines 166–180');
    expect(projectActivity([call('1', { path: 'world.js', from_line: 155, to_line: 165 })])[0].scope).toBe('requested lines 155–165');
    expect(projectActivity([call('1', { path: 'world.js', symbol: 'flushUpdates' })])[0].scope).toBe('symbol flushUpdates');
    expect(projectActivity([call('1', { path: 'world.js', offset: 0 })])[0].scope).toBe('requested byte 0');
  });

  it('pairs out-of-order results by ID and does not collapse overlapping calls', () => {
    const items = projectActivity([call('1'), call('3'), result('4', '3', false, 'failed'), result('2', '1', true, 'first')]);
    expect(items.map(item => [item.id, item.status, item.output])).toEqual([['1', 'ok', 'first'], ['3', 'fail', 'failed']]);
    const overlap = projectActivity([call('1'), call('3'), result('2', '1'), result('4', '3')]);
    expect(groupActivity(overlap)).toHaveLength(2);
  });

  it.each([
    ['same receipt version', 'a'.repeat(64), 'a'.repeat(64), 1],
    ['external source change', 'a'.repeat(64), 'b'.repeat(64), 2],
    ['known then unknown', 'a'.repeat(64), undefined, 2],
    ['unknown then known', undefined, 'a'.repeat(64), 2],
    ['both legacy unknown', undefined, undefined, 1],
  ])('handles %s without concealing version changes', (_, firstSHA, secondSHA, length) => {
    const events = reads();
    events[1].payload!.output = `[read ${JSON.stringify({ sha256: firstSHA, from_line: 155, to_line: 165 })}]\nfirst`;
    events[3].payload!.output = `[read ${JSON.stringify({ sha256: secondSHA, from_line: 166, to_line: 180 })}]\nsecond`;
    const items = projectActivity(events);
    expect(items.map(item => item.sourceSHA)).toEqual([firstSHA, secondSHA]);
    expect(groupActivity(items)).toHaveLength(length);
  });

  it('never pairs an orphan result by name when its recorded ID differs', () => {
    const items = projectActivity([call('1'), result('2', 'unknown')]);
    expect(items).toHaveLength(2);
    expect(items[0].status).toBe('run');
    expect(items[1].kind).toBe('result');
  });

  it('falls back for unambiguous legacy recordings without call IDs only', () => {
    const events = reads().slice(0, 2);
    for (const e of events) delete e.payload!.id;
    expect(projectActivity(events)[0].status).toBe('ok');
    const extra = call('3'); delete extra.payload!.id;
    expect(projectActivity([events[0], extra, events[1]]).map(item => item.kind)).toEqual(['tool', 'tool', 'result']);
  });

  it('breaks at step/recovery phase boundaries but not ordinary model/tool phases', () => {
    const state = (id: string, phase: string, step = 1, recovery_phase = 'diagnosing') =>
      event(id, 'run.state', { id: 'run1', status: 'running', phase, step, recovery_phase });
    const [a, b, c, d] = reads();
    const base = [state('0', 'tool_call'), a, b];
    expect(groupActivity(projectActivity([...base, state('5', 'model_call'), c, d]))).toHaveLength(1);
    expect(groupActivity(projectActivity([...base, state('5', 'tool_call', 2), c, d]))).toHaveLength(2);
    expect(groupActivity(projectActivity([...base, state('5', 'tool_call', 1, 'repairing'), c, d]))).toHaveLength(2);
    expect(groupActivity(projectActivity([...base, state('5', 'verifying'), c, d]))).toHaveLength(2);
  });

  it('keeps different runs apart even when tool-call IDs are reused', () => {
    const events = reads();
    events[2].payload = { ...events[2].payload, id: 'call1', run_id: 'run2' };
    events[3].payload = { ...events[3].payload, id: 'call1', run_id: 'run2' };
    const items = projectActivity(events);
    expect(items.map(item => item.result?.id)).toEqual(['2', '4']);
    expect(groupActivity(items)).toHaveLength(2);
  });
});
