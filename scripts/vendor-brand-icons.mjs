#!/usr/bin/env node
// Vendors the brand icons the Rig's module cards use:
//   node scripts/vendor-brand-icons.mjs  ->  panel/src/lib/brands/<id>.svg + brands.json
//
// The resolution order: a vendored
// icon first, else the brand's own site is asked for its vector mark —
// <link rel="icon|mask-icon"> or /favicon.svg, sanitised — and a neutral icon
// when nothing is found. A brand that publishes no vector mark at all
// (Typesense) keeps its official raster icon, downscaled, rather than a
// redrawn logo nobody at that project made. WHEN it runs matters: a
// client that already talks to a provider can resolve icons while it runs.
// Cerveau is local-first, so its panel never goes to the network for a picture;
// the resolution runs here, at development time, and the result is committed.
//
// A brand is one line below: an id, the words that identify it in a module's
// own strings (its title, model name, systemd unit, process name), and its
// site. Nothing in the panel names a brand; adding one is adding a line here
// and running this script.
import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync, readFileSync, existsSync, rmSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const out = join(root, 'panel/src/lib/brands');
const LOBE = 'https://unpkg.com/@lobehub/icons-static-svg@1.95.0/icons';   // MIT
const SIMPLE = 'https://cdn.jsdelivr.net/npm/simple-icons@15.0.0';          // CC0-1.0

const BRANDS = [
  { id: 'nvidia',      match: ['nvidia', 'nemotron'],                 site: 'https://www.nvidia.com' },
  { id: 'qwen',        match: ['qwen'],                               site: 'https://qwen.ai' },
  { id: 'vllm',        match: ['vllm'],                               site: 'https://vllm.ai',        local: 'panel/src/lib/engines/vllm.svg' },
  { id: 'llamacpp',    match: ['llama.cpp', 'llamacpp', 'llama-server'],                              local: 'panel/src/lib/engines/llamacpp.svg' },
  { id: 'huggingface', match: ['huggingface', 'hugging face'],        site: 'https://huggingface.co' },
  { id: 'ollama',      match: ['ollama'],                             site: 'https://ollama.com' },
  { id: 'typesense',   match: ['typesense'],                          site: 'https://typesense.org' },
  { id: 'docker',      match: ['docker', 'containerd'],               site: 'https://www.docker.com' },
  { id: 'python',      match: ['python'],                             site: 'https://www.python.org' },
  { id: 'comfyui',     match: ['comfy'],                              site: 'https://www.comfy.org' },
  // Cerveau's own mark, and its accent rather than a hex of its own
  { id: 'cerveau',     match: ['cerveau', 'crv'],                     local: 'panel/src/lib/logo.svg', color: 'var(--accent)' },
];

const get = async (url) => {
  const r = await fetch(url, { signal: AbortSignal.timeout(15000), redirect: 'follow', headers: { accept: 'text/html,image/svg+xml,*/*' } });
  if (!r.ok) throw new Error(`${r.status} ${url}`);
  return { text: await r.text(), url: r.url };
};
// a real svg, with nothing that runs or reaches out
const safeSvg = (t) => /^(?:<\?xml[^>]*>\s*)?(?:<!--[\s\S]*?-->\s*)*<svg[\s>]/i.test(t.trim()) && /<\/svg>\s*$/i.test(t.trim()) &&
  !/<(?:script|foreignObject|image)\b|\bon\w+\s*=|(?:href\s*=\s*["']\s*(?:https?:|\/\/|javascript:))|<!ENTITY/i.test(t);
const firstHex = (svg) => (svg.match(/(?:fill|stop-color)\s*[=:]\s*["']?\s*(#[0-9a-f]{6})\b/i) || [])[1];
// a colour that cannot be read on a near-black card is not a colour to use
const readable = (hex) => {
  if (!hex) return false;
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.16;
};
// one ink: the card tints the mark, so a mono icon must draw in currentColor
const mono = (svg) => {
  let s = svg.replace(/<\?xml[^>]*>\s*/i, '').replace(/<!--[\s\S]*?-->/g, '').replace(/<title>[\s\S]*?<\/title>/i, '').trim();
  if (!/currentColor/.test(s)) s = s.replace(/<svg\b/, '<svg fill="currentColor"');
  return s.replace(/\s(width|height)="[^"]*"/g, (m, k, off) => (off < s.indexOf('>') ? '' : m)) + '\n';
};

async function fromLobe(id) {
  const svg = (await get(`${LOBE}/${id}.svg`)).text;
  if (!safeSvg(svg)) throw new Error('unsafe');
  let color;
  try { color = firstHex((await get(`${LOBE}/${id}-color.svg`)).text); } catch { /* mono only */ }
  return { svg: mono(svg), color, source: `${LOBE}/${id}.svg`, license: 'MIT — LobeHub icons' };
}
let simpleData;
async function fromSimple(id) {
  const svg = (await get(`${SIMPLE}/icons/${id}.svg`)).text;
  if (!safeSvg(svg)) throw new Error('unsafe');
  simpleData ??= JSON.parse((await get(`${SIMPLE}/data/simple-icons.json`)).text);
  const entry = (Array.isArray(simpleData) ? simpleData : simpleData.icons).find((i) => (i.slug ?? i.title.toLowerCase().replace(/[^a-z0-9]/g, '')) === id);
  return { svg: mono(svg), color: entry?.hex ? `#${entry.hex}` : undefined, source: `${SIMPLE}/icons/${id}.svg`, license: 'CC0-1.0 — Simple Icons' };
}
// discovery: the site's own vector mark
async function fromSite(site) {
  const page = await get(site);
  const hrefs = [];
  for (const tag of page.text.matchAll(/<link\b[^>]*>/gi)) {
    const rel = (tag[0].match(/\brel\s*=\s*["']([^"']*)/i) || [])[1] || '';
    const href = (tag[0].match(/\bhref\s*=\s*["']([^"']*)/i) || [])[1];
    if (href && /(?:^|\s)(?:icon|shortcut|mask-icon)(?:\s|$)/i.test(rel) && /svg/i.test(tag[0])) hrefs.push(new URL(href.replace(/&amp;/g, '&'), page.url).href);
  }
  hrefs.push(new URL('/favicon.svg', page.url).href);
  for (const url of [...new Set(hrefs)].slice(0, 8)) {
    try {
      const svg = (await get(url)).text;
      if (safeSvg(svg)) return { svg: svg.replace(/<\?xml[^>]*>\s*/i, '').trim() + '\n', color: firstHex(svg), source: url, license: `the mark of ${new URL(site).hostname}, from its own site`, own: true };
    } catch { /* next candidate */ }
  }
  throw new Error('no vector mark on the site');
}

// Last resort: the site's raster icon. Pillow shrinks it to 96 px and reads
// the mark's own colour off it (the commonest bright, saturated pixel).
async function fromSiteRaster(site) {
  const page = await get(site);
  const hrefs = [];
  for (const tag of page.text.matchAll(/<link\b[^>]*>/gi)) {
    const rel = (tag[0].match(/\brel\s*=\s*["']([^"']*)/i) || [])[1] || '';
    const href = (tag[0].match(/\bhref\s*=\s*["']([^"']*)/i) || [])[1];
    if (href && /(?:^|\s)(?:icon|shortcut|apple-touch-icon)(?:\s|$)/i.test(rel) && /\.png(?:[?#]|$)/i.test(href)) hrefs.push(new URL(href, page.url).href);
  }
  hrefs.push(new URL('/favicon.png', page.url).href);
  for (const url of [...new Set(hrefs)].slice(0, 6)) {
    try {
      const r = await fetch(url, { signal: AbortSignal.timeout(15000) });
      if (!r.ok) continue;
      const tmp = join(out, '.raster.tmp');
      writeFileSync(tmp, Buffer.from(await r.arrayBuffer()));
      const py = `import sys
from PIL import Image
from collections import Counter
im=Image.open(sys.argv[1]).convert('RGBA')
if min(im.size)<48: sys.exit(3)
im.resize((96,96),Image.LANCZOS).save(sys.argv[2],optimize=True)
c=Counter()
for r,g,b,a in im.resize((64,64)).get_flattened_data() if hasattr(im,'get_flattened_data') else im.resize((64,64)).getdata():
    if a>200 and max(r,g,b)>120 and max(r,g,b)-min(r,g,b)>80: c[(r//8*8,g//8*8,b//8*8)]+=1
print('#%02x%02x%02x'%c.most_common(1)[0][0] if c else '')`;
      const png = join(out, '.raster.png');
      const color = execFileSync('python3', ['-W', 'ignore', '-c', py, tmp, png], { encoding: 'utf8' }).trim();
      const body = readFileSync(png);
      rmSync(tmp, { force: true }); rmSync(png, { force: true });
      return { png: body, color: color || undefined, source: url, license: `the icon of ${new URL(site).hostname}, from its own site`, own: true };
    } catch { /* next candidate */ }
  }
  throw new Error('no usable raster icon either');
}

mkdirSync(out, { recursive: true });
const index = {};
for (const b of BRANDS) {
  let got = null;
  const tries = b.local
    ? [async () => ({ svg: mono(readFileSync(join(root, b.local), 'utf8')), source: b.local, license: 'Cerveau — traced in this repository' })]
    : [() => fromLobe(b.id), () => fromSimple(b.id), () => fromSite(b.site), () => fromSiteRaster(b.site)];
  for (const t of tries) { try { got = await t(); break; } catch { /* next source */ } }
  if (!got) { console.log(`  —  ${b.id}: nothing found; modules of this brand keep the neutral icon`); continue; }
  if (got.png) writeFileSync(join(out, `${b.id}.png`), got.png);
  else writeFileSync(join(out, `${b.id}.svg`), got.svg);
  const color = b.color ?? (readable(got.color) ? got.color : undefined);
  // own: drawn in the brand's own colours, not tinted · raster: a picture, not a vector
  index[b.id] = { match: b.match, ...(color ? { color } : {}), ...(got.own ? { own: true } : {}), ...(got.png ? { raster: true } : {}), source: got.source, license: got.license };
  console.log(`  ✓  ${b.id.padEnd(12)} ${(color ?? 'no colour').padEnd(14)} ${got.source}`);
}
writeFileSync(join(out, 'brands.json'), JSON.stringify(index, null, 2) + '\n');
if (!existsSync(join(out, 'README.md'))) {
  writeFileSync(join(out, 'README.md'), `Brand marks for the Rig's module cards. Generated by \`node scripts/vendor-brand-icons.mjs\` —
edit the list there, not these files. \`brands.json\` records where each came from and under what
licence. The marks belong to their owners and are used only to identify their software.
`);
}
console.log(`wrote ${Object.keys(index).length} brands to ${out}`);
