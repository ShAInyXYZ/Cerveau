import { describe, it, expect } from 'vitest';
import { callSummary, toStep, mergeSteps, errorKey } from './steps';
import type { LiveStep } from './types';

describe('callSummary', () => {
  it('extracts the meaningful arg per tool', () => {
    expect(callSummary('bash', { command: 'go test ./...' })).toBe('go test ./...');
    expect(callSummary('edit', { path: 'a.go', old_string: 'x' })).toBe('a.go');
    expect(callSummary('serve', { action: 'start', dir: 'dist' })).toBe('dist');
    expect(callSummary('unknown_tool', { foo: 42, bar: 'hello' })).toBe('hello');
  });
});

describe('toStep + mergeSteps', () => {
  it('merges a result into its running call', () => {
    const call = toStep({ id: '1', type: 'tool.call', ts: '', payload: { name: 'bash', args: { command: 'ls' } } })!;
    const result = toStep({ id: '2', type: 'tool.result', ts: '', payload: { name: 'bash', ok: true, output: 'files' } })!;
    const merged = mergeSteps([call, result]);
    expect(merged).toHaveLength(1);
    expect(merged[0].status).toBe('ok');
    expect(merged[0].output).toBe('files');
  });

  it('keeps a failed result distinct in status', () => {
    const steps: LiveStep[] = [
      { id: '1', kind: 'tool', name: 'edit', arg: 'x', status: 'run' },
      { id: '2', kind: 'result', name: 'edit', status: 'fail', output: 'no match' },
    ];
    expect(mergeSteps(steps)[0].status).toBe('fail');
  });
});

describe('errorKey', () => {
  it('is stable for identical content and distinct by index', () => {
    const e = { class: 'guard', what: 'x failed' };
    expect(errorKey(e, 0)).toBe(errorKey(e, 0));
    expect(errorKey(e, 0)).not.toBe(errorKey(e, 1));
  });
});
