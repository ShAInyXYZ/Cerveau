import { describe, it, expect } from 'vitest';
import { splitStreaming, stripMarkdown } from './markdown-safe';

// Models write markdown everywhere, including places that are not a markdown
// surface. Rather than prompt them not to — which never holds — the panel
// handles it on this side.

describe('splitStreaming — what is safe to render mid-stream', () => {
  it('holds an unclosed code fence back as plain text', () => {
    const { ready, pending } = splitStreaming('Here you go:\n\n```js\nconst a = 1;');
    expect(ready).toBe('Here you go:\n');
    expect(pending).toContain('```js');
  });

  it('releases the fence once it closes', () => {
    const { ready, pending } = splitStreaming('Here:\n\n```js\nconst a = 1;\n```\n');
    expect(ready).toContain('```js');
    expect(pending).toBe('');
  });

  // A half-typed **bold reflows the paragraph when it completes.
  it('holds a paragraph with an unclosed emphasis run', () => {
    const { ready, pending } = splitStreaming('Done.\n\nThis is **impor');
    expect(ready).toBe('Done.\n');
    expect(pending).toBe('This is **impor');
  });

  it('releases a paragraph whose emphasis is balanced', () => {
    const { ready } = splitStreaming('This is **important** now.\n\nnext');
    expect(ready).toContain('**important**');
  });

  it('passes plain prose straight through', () => {
    const { ready, pending } = splitStreaming('Just a sentence.');
    expect(ready).toBe('Just a sentence.');
    expect(pending).toBe('');
  });

  it('handles empty input', () => {
    expect(splitStreaming('')).toEqual({ ready: '', pending: '' });
  });

  // An unmatched backtick is the most common half-token in code-heavy answers.
  it('holds an unclosed inline code span', () => {
    const { pending } = splitStreaming('Run `npm ins');
    expect(pending).toBe('Run `npm ins');
  });
});

describe('stripMarkdown — for surfaces that cannot render it', () => {
  it('unwraps bold, italic and inline code', () => {
    expect(stripMarkdown('Use **bold**, *soft* and `code` here'))
      .toBe('Use bold, soft and code here');
  });

  it('keeps link text and drops the URL', () => {
    expect(stripMarkdown('See [the docs](https://x.dev) now')).toBe('See the docs now');
  });

  it('drops heading markers', () => {
    expect(stripMarkdown('## Which engine?')).toBe('Which engine?');
  });

  it('leaves ordinary punctuation alone', () => {
    expect(stripMarkdown('2 * 3 = 6, and 5_000 is a number'))
      .toBe('2 * 3 = 6, and 5_000 is a number');
  });

  it('survives an unterminated marker without eating text', () => {
    expect(stripMarkdown('This is **not closed')).toBe('This is **not closed');
  });

  it('handles empty and undefined', () => {
    expect(stripMarkdown('')).toBe('');
    expect(stripMarkdown(undefined)).toBe('');
  });
});
