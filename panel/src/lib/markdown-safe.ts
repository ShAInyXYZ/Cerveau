/**
 * Markdown sanitising, done on OUR side.
 *
 * Models write markdown everywhere — including into a question prompt, a tool
 * argument, or a half-finished stream. Asking them not to never holds: it is
 * how they are trained to write. So the panel handles it here instead of
 * spending prompt tokens on an instruction that fails silently.
 *
 * Two problems, two functions:
 *
 *   splitStreaming  a surface that CAN render markdown, but not yet — the text
 *                   is still arriving and half a construct renders broken and
 *                   then reflows when it completes.
 *
 *   stripMarkdown   a surface that CANNOT render markdown at all — a question
 *                   title, a button label — where the source shows through as
 *                   literal **asterisks**.
 */

/** A fence line: ``` or ~~~ with optional info string. */
const FENCE = /^\s*(`{3,}|~{3,})/;

/**
 * Split streaming markdown into the part that is safe to render now and the
 * tail that is still being written.
 *
 * The rule is block-level: everything up to the last COMPLETE block is ready.
 * A block is incomplete if it opens a fence that never closes, or if its
 * inline markers are unbalanced.
 */
export function splitStreaming(src: string): { ready: string; pending: string } {
  if (!src) return { ready: '', pending: '' };

  const lines = src.split('\n');

  // 1. An unclosed fence swallows everything after it. Find the last fence
  //    that has no partner and hold from there.
  let fenceStart = -1;
  let open = false;
  for (let i = 0; i < lines.length; i++) {
    if (FENCE.test(lines[i])) {
      if (!open) { open = true; fenceStart = i; }
      else { open = false; fenceStart = -1; }
    }
  }
  if (open && fenceStart >= 0) {
    return {
      ready: lines.slice(0, fenceStart).join('\n'),
      pending: lines.slice(fenceStart).join('\n'),
    };
  }

  // 2. Otherwise hold only the trailing paragraph, and only if its inline
  //    markers are still unbalanced. A finished paragraph is safe even while
  //    more text is coming, because a blank line ends it for good.
  const lastBreak = src.lastIndexOf('\n\n');
  const head = lastBreak >= 0 ? src.slice(0, lastBreak + 1) : '';
  const tail = lastBreak >= 0 ? src.slice(lastBreak + 2) : src;

  return balanced(tail)
    ? { ready: src, pending: '' }
    : { ready: head, pending: tail };
}

/**
 * Whether a fragment's inline markers are closed.
 *
 * Counted, not parsed: a real parser would be right more often and cost far
 * more than the problem is worth. The failure mode of guessing wrong is one
 * extra frame of plain text, which is invisible.
 */
function balanced(s: string): boolean {
  const ticks = (s.match(/`/g) ?? []).length;
  if (ticks % 2 !== 0) return false;
  // strip inline code before counting emphasis — asterisks inside `a * b` are
  // arithmetic, not markup
  const bare = s.replace(/`[^`]*`/g, '');
  const bold = (bare.match(/\*\*/g) ?? []).length;
  if (bold % 2 !== 0) return false;
  const single = (bare.replace(/\*\*/g, '').match(/\*/g) ?? []).length;
  return single % 2 === 0;
}

/**
 * Render markdown down to plain text, for surfaces that cannot render it.
 *
 * Conservative by design: an unterminated marker is left exactly as written
 * rather than guessed at. Showing a stray `**` is a cosmetic flaw; eating the
 * words after it is a lie about what the model said.
 */
export function stripMarkdown(src: string | undefined | null): string {
  if (!src) return '';
  return src
    // [text](url) -> text.  The URL is dropped: a surface that cannot render
    // markdown cannot render a link either, and a bare URL is noise.
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    // `code` -> code
    .replace(/`([^`\n]+)`/g, '$1')
    // **bold** / __bold__ -> bold
    .replace(/\*\*([^*\n]+)\*\*/g, '$1')
    .replace(/__([^_\n]+)__/g, '$1')
    // *italic* -> italic. Requires a non-space neighbour so "2 * 3" survives.
    .replace(/\*(\S[^*\n]*?\S|\S)\*/g, '$1')
    // leading heading markers and blockquote arrows
    .replace(/^\s{0,3}#{1,6}\s+/gm, '')
    .replace(/^\s{0,3}>\s?/gm, '')
    .trim();
}
