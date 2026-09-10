import { describe, expect, it } from 'vitest';
import { incidentView, displayedStep } from './incidentView';
import type { PlanState, RunState } from '../types';

const plan = { blocked: 1, done: false, steps: [{title:'Storage'}, {title:'Lighting', verdict:{pass:false,evidence:'Error: block light was 0'}}] } as PlanState;
describe('incident presentation', () => {
  it('uses the blocked prerequisite, not the last executing step', () => {
    const run = {status:'suspended',step:2,reason:'plan_blocked'} as RunState;
    expect(displayedStep(run,plan,false)).toBe(1);
    const view = incidentView({what:'plan_blocked',class:'suspended'},run,plan);
    expect(view.title).toBe('Step 2 needs repair');
    expect(view.subject).toBe('Lighting');
    expect(view.detail).toContain('Error: block light was 0');
    expect(view.detail).toContain('plan_blocked');
  });
  it('does not attach an old plan failure to an unrelated failed chat', () => {
    const run = {id:'chat',status:'failed',step:-1,phase:'model_call',reason:'model_error'} as RunState;
    expect(displayedStep(run,plan,false)).toBe(-1);
    expect(incidentView({what:'Core unavailable',why:'connection refused'},run,plan).title).toBe('Core unavailable');
    expect(incidentView({run_id:'old',what:'plan_blocked'},run,plan).subject).toBe('');
  });
  it('preserves the active execution cursor and keeps Reflex errors separate', () => {
    const run = {status:'running',step:2} as RunState;
    expect(displayedStep(run,plan,true)).toBe(2);
    const view = incidentView({what:'Reflex failed',why:'permission denied'},{...run,kind:'reflex'},plan);
    expect(view.title).toBe('Reflex failed');
    expect(view.detail).toContain('permission denied');
    expect(view.subject).toBe('');
  });
  it('does not discard full technical detail or confuse model rounds with repeats', () => {
    const view = incidentView({what:'plan_blocked',why:'guard: iteration cap (32) reached',tried:'repair A'},null,plan);
    expect(view.explanation).toContain('model-round');
    expect(view.detail).toContain('repair A');
  });
});
