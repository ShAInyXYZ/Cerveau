/** How close to the bottom still counts as "following the tail". */
export const STICK_PX = 80;

/**
 * Whether the stream should scroll to the tail.
 *
 * Measured from the scroller as it stands BEFORE new content lands, so a user
 * who has scrolled up to read is left where they are.
 */
export function shouldFollow(m: {
  scrollHeight: number;
  scrollTop: number;
  clientHeight: number;
}): boolean {
  return m.scrollHeight - m.scrollTop - m.clientHeight < STICK_PX;
}
