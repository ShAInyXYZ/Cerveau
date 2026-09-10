import type { PlanState, RunState, SessionError } from '../types';

function isPlanRun(run: RunState | null): boolean {
  return !!run && run.kind !== 'reflex' && (run.reason === 'plan_blocked' || run.phase === 'step' || !!run.recovery_phase);
}

export function displayedStep(run: RunState | null, plan: PlanState | null, running: boolean): number {
  if (!running && isPlanRun(run) && plan && plan.blocked >= 0) return plan.blocked;
  return run?.step ?? -1;
}

export function incidentView(error: SessionError, run: RunState | null, plan: PlanState | null) {
  const matchesRun = !error.run_id || !run?.id || error.run_id === run.id;
  const planIncident = matchesRun && run?.kind !== 'reflex' && (error.what === 'plan_blocked' || isPlanRun(run));
  const blocked = planIncident && plan && plan.blocked >= 0 ? plan.blocked : -1;
  const step = blocked >= 0 ? plan?.steps[blocked] : undefined;
  const raw = [error.class, error.what, error.why || error.detail, error.tried].filter(Boolean).join('\n\n');
  const verdict = step?.verdict;
  const stop = verdict?.execution_stop || error.why || error.detail || '';
  const detail = [verdict?.evidence, verdict?.evidence_event_id ? `Full check output: ${verdict.evidence_event_id}` : '', verdict?.execution_stop, raw].filter(Boolean).join('\n\n');
  const diagnostic = verdict?.evidence?.split('\n').find(line => /(?:Error|Exception|AssertionError):/.test(line)) ?? '';
  return {
    title: step ? `Step ${blocked + 1} needs repair` : (error.what || 'The run stopped').replaceAll('_', ' '),
    subject: step?.title ?? '',
    explanation: /iteration cap|model.round limit/i.test(stop)
      ? 'Recovery reached its model-round limit. Saved work and failure evidence are kept.'
      : step ? 'The plan is paused here. Recover this step, recheck affected work, then continue.' : '',
    detail,
    diagnostic: diagnostic.length > 300 ? diagnostic.slice(0,300) + '…' : diagnostic,
    recoverable: !!step || error.class === 'suspended',
  };
}
