import { describe, expect, it } from 'vitest';
import { retryLabel } from './retryAction';
import type { RunState, PlanState } from '../types';
describe('action-specific retry', () => {
  it('never retries chat or a plan for a manual Reflex incident', () => {
    expect(retryLabel({ kind: 'reflex' } as RunState, { done: true } as PlanState)).toBeNull();
    expect(retryLabel({ kind: 'reflex' } as RunState, { done: false } as PlanState)).toBeNull();
  });
  it('does not describe a completed plan as unfinished', () => {
    expect(retryLabel(null, { done: true } as PlanState)).toBe('Retry message');
    expect(retryLabel(null, { done: false } as PlanState)).toBe('Recover step and continue plan');
  });
});
