// Inspect the exact stylesheets referenced by the production entry point.
// Vite development CSS is not minified and cannot catch prefix-loss regressions.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

const dist = fileURLToPath(new URL('../internal/panel/dist/', import.meta.url));
const html = readFileSync(resolve(dist, 'index.html'), 'utf8');
const stylesheets = [...html.matchAll(/<link\b[^>]*>/g)]
  .filter(([tag]) => /\brel=["']stylesheet["']/.test(tag))
  .map(([tag]) => tag.match(/\bhref=["']([^"']+)["']/)?.[1]);
assert.ok(stylesheets.length, 'Build the panel first: production index.html must reference CSS');
const css = stylesheets.map(href => {
  assert.ok(href && !/^[a-z]+:/i.test(href), 'Production CSS must be a local asset');
  const path = resolve(dist, href.replace(/^\.?\//, ''));
  assert.ok(path.startsWith(resolve(dist) + sep), 'Production CSS must stay in dist');
  return readFileSync(path, 'utf8');
}).join('\n');

// These declarations are plain leaf rules, including after Svelte scoping and
// CSS minification. Match class tokens rather than generated scope hashes.
const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
  .flatMap(([, selectors, declarations]) => selectors.split(',')
    .map(selector => ({ selector: selector.trim(), declarations })));
const hasClass = (selector, name) => new RegExp(`\\.${name}(?![\\w-])`).test(selector);
const baseRules = rules.filter(({ selector }) =>
  !/:(?:hover|focus|active|disabled|before|after)\b/.test(selector));

function standardBlur(declarations, expected, label) {
  const values = declarations.split(';').map(value => value.trim())
    .filter(value => /^backdrop-filter\s*:/.test(value))
    .map(value => value.slice(value.indexOf(':') + 1).replace(/\s+/g, ''));
  assert.equal(values.at(-1), `blur(${expected}px)`,
    `${label} must retain standard backdrop-filter: blur(${expected}px) in production CSS; a -webkit-only declaration does not blur in current Chromium. Emitted declarations: ${declarations}`);
}

test('production chat transition is a 160px opacity gradient without blur or masking', () => {
  const matches = baseRules.filter(({ selector }) => hasClass(selector, 'stream-fade'));
  assert.ok(matches.length, 'Production .stream-fade rule is missing');
  const declarations = matches.map(rule => rule.declarations).join(';');
  const values = Object.fromEntries(declarations.split(';').filter(Boolean).map(value => {
    const colon = value.indexOf(':');
    return [value.slice(0, colon).trim(), value.slice(colon + 1).replace(/\s+/g, '')];
  }));
  assert.equal(values.height, '160px');
  assert.match(values.background ?? values['background-image'] ?? '',
    /^linear-gradient\((?:tobottom,|180deg,)?(?:transparent|#0000)(?:0%)?,var\(--bg\)(?:100%)?\)$/,
    'Transition must fade simply from transparent at the top to the background at the bottom');
  for (const property of ['filter', 'backdrop-filter', '-webkit-backdrop-filter', 'mask-image', '-webkit-mask-image', 'mask', '-webkit-mask']) {
    assert.ok(values[property] === undefined || values[property] === 'none',
      `The opacity-only transition must not apply ${property}`);
  }
});

test('production jump control retains the standard 16px backdrop blur', () => {
  const matches = baseRules.filter(({ selector }) =>
    hasClass(selector, 'latest') && hasClass(selector, 'ib'));
  assert.ok(matches.length, 'Production .latest .ib rule is missing');
  standardBlur(matches.map(rule => rule.declarations).join(';'), 16, '.latest .ib');
});

test('the production CSS guard rejects prefixed-only and overridden blur', () => {
  assert.throws(() => standardBlur('-webkit-backdrop-filter:blur(24px)', 24, 'fixture'));
  assert.throws(() => standardBlur('backdrop-filter:blur(24px);backdrop-filter:none', 24, 'fixture'));
  standardBlur('-webkit-backdrop-filter:blur(24px);backdrop-filter:blur(24px)', 24, 'fixture');
});
