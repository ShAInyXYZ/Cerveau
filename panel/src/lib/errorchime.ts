/**
 * Decides whether an error is worth a sound.
 *
 * The old rule was `errors.length > previousLength`, which conflates two very
 * different things: an error ARRIVING, and an error being DISPLAYED. Selecting
 * a session resets the list to empty, so opening a session that ended on a
 * guard stop counted 0 -> 2 and played the alert — announcing something that
 * happened hours ago, every single time you looked at it.
 *
 * Tracking ids instead of a count makes the question answerable: has this
 * client ever seen THIS error before? History adopted on first sight is silent;
 * only something genuinely new makes a noise.
 */
export class ErrorChime {
  /** every error id this client has seen, across session switches */
  private seen = new Set<string>();
  /** false until the first list arrives, so existing history is adopted quietly */
  private primed = false;

  /**
   * Call with the ids currently displayed. Returns true if at least one of them
   * is new AND this is not the first sight of the session.
   */
  shouldPlay(ids: string[]): boolean {
    let fresh = false;
    for (const id of ids) {
      if (!this.seen.has(id)) {
        this.seen.add(id);
        fresh = true;
      }
    }
    if (!this.primed) {
      this.primed = true;   // first look: adopt whatever is already there
      return false;
    }
    return fresh;
  }

  /**
   * Called on a session switch. Deliberately does NOT clear `seen` — an error
   * you were already shown is not new again just because you looked away, and
   * re-announcing it on return is the bug this class exists to prevent. Only
   * the priming flag resets, so a session opened for the first time is silent.
   */
  reset(): void {
    this.primed = false;
  }
}
