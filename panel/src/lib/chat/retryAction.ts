import type { RunState, PlanState } from '../types';

// A manual Reflex is not a continuation of the last chat prompt or plan.
export function retryLabel(run: RunState | null, plan: PlanState | null): string | null {
  if (run?.kind === 'reflex') return null;
  return plan && !plan.done ? 'Recover step and continue plan' : 'Retry message';
}
