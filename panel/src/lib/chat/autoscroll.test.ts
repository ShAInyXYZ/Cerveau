import { describe, it, expect } from 'vitest';
import { shouldFollow, STICK_PX } from './autoscroll';

// Autoscroll used to fire unconditionally, so scrolling up during a long build
// meant the next message yanked you back to the bottom — at exactly the moment
// you most want to read earlier output.
describe('shouldFollow', () => {
  it('follows when parked at the bottom', () => {
    expect(shouldFollow({ scrollHeight: 1000, scrollTop: 800, clientHeight: 200 })).toBe(true);
  });

  it('follows within the stick threshold', () => {
    // 1000 - 760 - 200 = 40px from the bottom
    expect(shouldFollow({ scrollHeight: 1000, scrollTop: 760, clientHeight: 200 })).toBe(true);
  });

  it('does NOT follow once the user has scrolled up to read', () => {
    expect(shouldFollow({ scrollHeight: 1000, scrollTop: 100, clientHeight: 200 })).toBe(false);
  });

  it('does not follow just past the threshold', () => {
    // exactly STICK_PX away is not "close enough"
    expect(shouldFollow({ scrollHeight: 1000, scrollTop: 800 - STICK_PX, clientHeight: 200 })).toBe(false);
  });

  // A container that has not overflowed yet is trivially at its bottom, and a
  // brand-new session must still scroll as messages arrive.
  it('follows when there is nothing to scroll', () => {
    expect(shouldFollow({ scrollHeight: 200, scrollTop: 0, clientHeight: 200 })).toBe(true);
  });
});
