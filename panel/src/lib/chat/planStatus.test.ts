import { describe, expect, it } from 'vitest';
import { planCounts, planStatusLabel } from './planStatus';

describe('plan evidence labels', () => {
  it('separates stale passes, a blocker and unstarted work', () => {
    const steps = ['needs_reverify','needs_reverify','blocked',...Array(5).fill('pending')].map(status => ({status}));
    expect(planCounts(steps)).toEqual([
      {status:'needs_reverify', count:2, label:'awaiting recheck'},
      {status:'blocked', count:1, label:'blocked'},
      {status:'pending', count:5, label:'not started'},
    ]);
    expect(planStatusLabel('needs_reverify')).toBe('awaiting recheck');
  });
  it('does not count active, unknown or unverified states as pending', () => {
    expect(planCounts(['done','running','verifying','unverified','unknown'].map(status => ({status})))).toEqual([
      {status:'done',count:1,label:'verified'},
      {status:'running',count:1,label:'running'},
      {status:'verifying',count:1,label:'checking'},
      {status:'unverified',count:2,label:'unverified'},
    ]);
  });
  it('normalizes server and report status aliases without losing evidence', () => {
    expect(planCounts([{status:'passed'}, {status:'failed'}])).toEqual([
      {status:'done',count:1,label:'verified'}, {status:'blocked',count:1,label:'blocked'},
    ]);
    expect(planCounts([])).toEqual([]);
  });
});
