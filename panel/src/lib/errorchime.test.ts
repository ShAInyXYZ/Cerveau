import { describe, it, expect } from 'vitest';
import { ErrorChime } from './errorchime';

// The error sound fired whenever the error COUNT grew. Selecting a session
// resets that count to 0, so opening a session that ended on a guard stop
// replayed its error out loud — a sound announcing something that happened
// hours ago.
//
// The rule is "play on happening", not "play on display": a sound is an alert
// about a NEW event, and an event this client has already seen is not new.
describe('ErrorChime', () => {
  it('stays silent when opening a session with old errors', () => {
    const chime = new ErrorChime();
    // first sight of this session's history — two errors that already happened
    expect(chime.shouldPlay(['evt_5', 'evt_9'])).toBe(false);
  });

  it('plays when a genuinely new error arrives', () => {
    const chime = new ErrorChime();
    chime.shouldPlay(['evt_5']);                       // adopt history silently
    expect(chime.shouldPlay(['evt_5', 'evt_9'])).toBe(true);
  });

  it('does not replay an error it has already announced', () => {
    const chime = new ErrorChime();
    chime.shouldPlay(['evt_5']);
    expect(chime.shouldPlay(['evt_5', 'evt_9'])).toBe(true);
    expect(chime.shouldPlay(['evt_5', 'evt_9'])).toBe(false);
  });

  // Switching away and back is still the same errors — the count drops to zero
  // in between, which is exactly what fooled the old check.
  it('stays silent when returning to a session', () => {
    const chime = new ErrorChime();
    chime.shouldPlay(['evt_5', 'evt_9']);
    chime.reset();                                     // session switch
    expect(chime.shouldPlay(['evt_5', 'evt_9'])).toBe(false);
  });

  // Deliberately silent. An error that arrived while you were looking at
  // ANOTHER session is not "happening" from your point of view — you cannot see
  // what it refers to. The error card is still there to be read; the sound is
  // reserved for something occurring in front of you. The alternative is a
  // chime that fires on arrival at a screen, which is the bug being fixed.
  it('is silent for an error that arrived while you were on another session', () => {
    const chime = new ErrorChime();
    chime.shouldPlay(['evt_5']);
    chime.reset();
    expect(chime.shouldPlay(['evt_5', 'evt_9'])).toBe(false);
    // ...but the NEXT one, now that you are here, does sound
    expect(chime.shouldPlay(['evt_5', 'evt_9', 'evt_12'])).toBe(true);
  });

  it('handles a dismissed error without re-announcing the rest', () => {
    const chime = new ErrorChime();
    chime.shouldPlay(['evt_5', 'evt_9']);
    expect(chime.shouldPlay(['evt_9'])).toBe(false);   // evt_5 dismissed
  });
});
