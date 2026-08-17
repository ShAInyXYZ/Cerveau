import { describe, it, expect } from 'vitest';

/**
 * Regression guard for a bug that has now bitten three times: an $effect that
 * reads a POLLED value re-runs on every poll and undoes the user's state.
 *
 *   plan strip  — effect read `report` (polls) and cancelled its own timers
 *   picker      — effect read `current` (polls) and reset the browsed path
 *
 * The rule: an effect that reacts to "the dialog opened" must depend on the
 * OPEN FLAG ALONE, and guard re-entry with a non-reactive latch.
 *
 * This models that logic so the invariant is tested, not just the instance.
 */
function makeOpenOnceEffect(load: (p: string) => void) {
  let opened = false; // plain variable — not reactive
  return function run(open: boolean, current: string) {
    if (open) {
      if (!opened) { opened = true; load(current); }
    } else {
      opened = false;
    }
  };
}

describe('open-once effect', () => {
  it('loads once per opening, not once per poll', () => {
    const loaded: string[] = [];
    const run = makeOpenOnceEffect((p) => loaded.push(p));

    run(true, '/home/user/project');          // dialog opens
    run(true, '/home/user/project');          // health poll ticks
    run(true, '/home/user/project');          // …and again
    expect(loaded).toEqual(['/home/user/project']);
  });

  it('loads again after a genuine close/reopen', () => {
    const loaded: string[] = [];
    const run = makeOpenOnceEffect((p) => loaded.push(p));

    run(true, '/a');
    run(false, '/a');   // closed
    run(true, '/b');    // reopened somewhere else
    expect(loaded).toEqual(['/a', '/b']);
  });

  it('never reloads while the user is navigating', () => {
    const loaded: string[] = [];
    const run = makeOpenOnceEffect((p) => loaded.push(p));

    run(true, '/home/user/deep/path');
    // user taps "up" three times; the polled `current` never changes
    for (let i = 0; i < 3; i++) run(true, '/home/user/deep/path');
    expect(loaded).toHaveLength(1);
  });
});
