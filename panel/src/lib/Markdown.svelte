<script>
  import { marked } from 'marked';
  import markedFootnote from 'marked-footnote';
  import hljs from 'highlight.js/lib/common';

  // Chat passes `source`; keep `text` as an alias so either prop name works.
  let { source = '', text = '' } = $props();

  marked.setOptions({ breaks: true, gfm: true });
  marked.use(markedFootnote());

  // ==highlight== — a popular non-standard extension (marked doesn't ship it).
  marked.use({
    extensions: [{
      name: 'highlight',
      level: 'inline',
      start(src) { return src.indexOf('=='); },
      tokenizer(src, tokens) {
        // require exactly ==...== — not part of a === run (setext-ish noise the
        // model sometimes emits), and content that doesn't start/end with '='.
        const m = /^==(?!=)([^=][\s\S]*?[^=]|[^=])==(?!=)/.exec(src);
        if (m) return { type: 'highlight', raw: m[0], tokens: this.lexer.inlineTokens(m[1]) };
      },
      renderer(token) { return `<mark>${this.parser.parseInline(token.tokens)}</mark>`; }
    }]
  });

  // marked v18 hands each renderer a single token object, NOT positional args.
  const renderer = {
    code({ text: code, lang }) {
      const valid = lang && hljs.getLanguage(lang);
      const highlighted = valid
        ? hljs.highlight(code, { language: lang }).value
        : hljs.highlightAuto(code).value;
      const label = valid ? lang : '';
      return `<div class="md-codeblock">`
        + `<div class="md-codebar"><span class="md-lang">${label}</span>`
        + `<button class="md-copy" type="button" data-code="${encodeURIComponent(code)}">copy</button></div>`
        + `<pre class="md-code"><code>${highlighted}</code></pre></div>`;
    },
    codespan({ text: code }) {
      return `<code class="md-inline">${code}</code>`;
    },
    link({ href, title, tokens }) {
      const inner = this.parser.parseInline(tokens);
      const t = title ? ` title="${title}"` : '';
      return `<a href="${href}"${t} target="_blank" rel="noopener noreferrer">${inner}</a>`;
    }
  };
  marked.use({ renderer });

  // Models habitually wrap illustrative markdown in a ```markdown fence — which
  // makes a viewer show it as raw source instead of rendering it. The `markdown`
  // (or `md`) language tag specifically signals "this IS markdown", so we unwrap
  // such fences wherever they appear and let it render for real.
  //
  // The inner content itself may contain nested ``` fences (a code block shown
  // inside the markdown demo). We match the ```markdown opener greedily to the
  // LAST ``` in the string so those inner fences survive as content, then
  // re-indent the inner block to column 0 so its own fences parse correctly.
  function unwrap(s) {
    const re = /```(?:markdown|md)[ \t]*\r?\n([\s\S]*)\r?\n[ \t]*```/i;
    return s.replace(re, (_, inner) => {
      // strip the common leading indentation the model may have added
      const lines = inner.split('\n');
      const indents = lines.filter((l) => l.trim()).map((l) => l.match(/^[ \t]*/)[0].length);
      const min = indents.length ? Math.min(...indents) : 0;
      return lines.map((l) => l.slice(min)).join('\n');
    });
  }

  let src = $derived(unwrap(source || text || ''));
  let html = $derived(marked.parse(src));

  // Per-codeblock copy, via event delegation on the rendered HTML.
  function onClick(e) {
    const btn = e.target.closest('.md-copy');
    if (!btn) return;
    navigator.clipboard.writeText(decodeURIComponent(btn.dataset.code ?? ''));
    const prev = btn.textContent;
    btn.textContent = 'copied';
    btn.classList.add('done');
    setTimeout(() => { btn.textContent = prev; btn.classList.remove('done'); }, 1200);
  }
</script>

<div class="md-body" role="presentation" onclick={onClick}>{@html html}</div>

<style>
  .md-body {
    font-size: 14px;
    line-height: 1.65;
    color: var(--text);
    word-break: break-word;
  }

  /* vertical rhythm — collapse first/last margins so bubbles hug tight */
  .md-body :global(> *) { margin: 0 0 12px; }
  .md-body :global(> *:last-child) { margin-bottom: 0; }

  /* headings */
  .md-body :global(h1),
  .md-body :global(h2),
  .md-body :global(h3),
  .md-body :global(h4) {
    color: var(--text);
    font-weight: 650;
    line-height: 1.3;
    margin: 20px 0 10px;
    letter-spacing: -0.01em;
  }
  .md-body :global(> h1:first-child),
  .md-body :global(> h2:first-child),
  .md-body :global(> h3:first-child) { margin-top: 0; }
  .md-body :global(h1) { font-size: 1.5em; padding-bottom: .3em; border-bottom: 1px solid var(--line2); }
  .md-body :global(h2) { font-size: 1.28em; padding-bottom: .25em; border-bottom: 1px solid var(--line); }
  .md-body :global(h3) { font-size: 1.12em; }
  .md-body :global(h4) { font-size: 1em; color: var(--muted); }

  /* text */
  .md-body :global(strong) { color: var(--text); font-weight: 680; }
  .md-body :global(em) { font-style: italic; color: var(--text); }
  .md-body :global(del) { color: var(--dim); }
  .md-body :global(a) {
    color: var(--accent);
    text-decoration: none;
    border-bottom: 1px solid var(--accent-line);
    transition: border-color .12s;
  }
  .md-body :global(a:hover) { border-bottom-color: var(--accent); }

  /* lists */
  .md-body :global(ul),
  .md-body :global(ol) { padding-left: 1.4em; }
  .md-body :global(li) { margin: 4px 0; }
  .md-body :global(li::marker) { color: var(--dim); }
  .md-body :global(ul li::marker) { color: var(--accent); }
  .md-body :global(li > ul),
  .md-body :global(li > ol) { margin: 4px 0 0; }
  .md-body :global(input[type="checkbox"]) { accent-color: var(--accent); margin-right: 6px; }

  /* blockquote */
  .md-body :global(blockquote) {
    margin: 12px 0;
    padding: 2px 14px;
    border-left: 2px solid var(--accent-line);
    background: color-mix(in srgb, var(--accent) 5%, transparent);
    border-radius: 0 6px 6px 0;
    color: var(--muted);
  }
  .md-body :global(blockquote > *:last-child) { margin-bottom: 0; }

  /* inline code */
  .md-body :global(code.md-inline) {
    font-family: var(--font-mono);
    font-size: .86em;
    padding: 1.5px 5px;
    border-radius: 5px;
    background: var(--s3);
    color: #f0b088;
    border: 1px solid var(--line2);
    white-space: nowrap;
  }

  /* fenced code block */
  .md-body :global(.md-codeblock) {
    margin: 12px 0;
    border-radius: 9px;
    overflow: hidden;
    background: var(--s1);
    border: 1px solid var(--line);
  }
  .md-body :global(.md-codebar) {
    display: flex; align-items: center; justify-content: space-between;
    height: 30px; padding: 0 6px 0 12px;
    background: color-mix(in srgb, #000 14%, var(--s1));
    border-bottom: 1px solid var(--line);
  }
  .md-body :global(.md-lang) {
    font-family: var(--font-mono); font-size: 10px;
    letter-spacing: .1em; text-transform: uppercase; color: var(--dim);
  }
  .md-body :global(.md-copy) {
    font-family: var(--font-mono); font-size: 10.5px;
    color: var(--dim); background: transparent;
    border: 1px solid transparent; border-radius: 6px;
    padding: 3px 8px; cursor: pointer;
    transition: color .12s, background .12s;
  }
  .md-body :global(.md-copy:hover) { color: var(--text); background: var(--s3); }
  .md-body :global(.md-copy.done) { color: var(--ok); }
  .md-body :global(pre.md-code) {
    margin: 0; padding: 12px 14px; overflow-x: auto;
    font-family: var(--font-mono); font-size: 12.5px; line-height: 1.55;
  }
  .md-body :global(pre.md-code code) { background: none; border: none; padding: 0; color: var(--text); }

  /* tables */
  .md-body :global(table) {
    border-collapse: collapse; width: 100%; font-size: 13px;
    margin: 12px 0; overflow: hidden; border-radius: 8px;
    border: 1px solid var(--line);
  }
  .md-body :global(th),
  .md-body :global(td) { padding: 7px 12px; text-align: left; border-bottom: 1px solid var(--line); }
  .md-body :global(th) {
    background: var(--s2); color: var(--muted);
    font-weight: 600; font-size: 11px; letter-spacing: .04em; text-transform: uppercase;
  }
  .md-body :global(tr:last-child td) { border-bottom: none; }
  .md-body :global(tbody tr:hover) { background: color-mix(in srgb, var(--accent) 4%, transparent); }

  /* rule + images */
  .md-body :global(hr) { border: none; height: 1px; background: var(--line2); margin: 18px 0; }
  .md-body :global(img) { max-width: 100%; border-radius: 8px; margin: 6px 0; }

  /* highlight (==x== and <mark>) — amber wash in-theme, not raw yellow */
  .md-body :global(mark) {
    background: color-mix(in srgb, var(--accent) 30%, transparent);
    color: var(--text);
    padding: 0 3px; border-radius: 3px;
  }

  /* details / summary */
  .md-body :global(details) {
    margin: 12px 0; padding: 8px 12px;
    background: var(--s1); border: 1px solid var(--line); border-radius: 8px;
  }
  .md-body :global(summary) { cursor: pointer; color: var(--muted); font-weight: 550; }
  .md-body :global(summary:hover) { color: var(--text); }
  .md-body :global(details[open] summary) { margin-bottom: 8px; }

  /* footnotes */
  .md-body :global(sup a),
  .md-body :global(a[data-footnote-ref]) {
    font-size: .75em; color: var(--accent); border: none;
    padding: 0 2px; text-decoration: none;
  }
  .md-body :global(.footnotes) {
    margin-top: 18px; padding-top: 12px;
    border-top: 1px solid var(--line); font-size: 12.5px; color: var(--muted);
  }
  .md-body :global(.footnotes ol) { padding-left: 1.2em; }
  .md-body :global(a[data-footnote-backref]) { border: none; text-decoration: none; }

  /* highlight.js — warm-charcoal token palette */
  .md-body :global(.hljs-comment), .md-body :global(.hljs-quote) { color: var(--faint); font-style: italic; }
  .md-body :global(.hljs-keyword), .md-body :global(.hljs-selector-tag), .md-body :global(.hljs-built_in) { color: #e08c4e; }
  .md-body :global(.hljs-string), .md-body :global(.hljs-attr) { color: #8fbf8f; }
  .md-body :global(.hljs-number), .md-body :global(.hljs-literal) { color: #c99a5b; }
  .md-body :global(.hljs-title), .md-body :global(.hljs-function .hljs-title), .md-body :global(.hljs-section) { color: #d6a8c4; }
  .md-body :global(.hljs-variable), .md-body :global(.hljs-name), .md-body :global(.hljs-property) { color: var(--text); }
  .md-body :global(.hljs-type), .md-body :global(.hljs-class .hljs-title) { color: #6bb7c9; }
  .md-body :global(.hljs-meta), .md-body :global(.hljs-symbol) { color: var(--dim); }
</style>
