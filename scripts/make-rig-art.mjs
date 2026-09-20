#!/usr/bin/env node
// Regenerates the Rig's component illustrations:
//   node scripts/make-rig-art.mjs            -> panel/src/lib/settings/rig/art/*.svg
//   node scripts/make-rig-art.mjs --preview  -> also docs-private/rig-art-preview.html
//
// Same language as cerveau-site's models as that page renders them: a flat
// fill, a hairline on every edge, fans that turn. The GPU is traced from
// cerveau-site/site/assets/models/gpu.glb, front view, 100 px per model unit:
// a 2.66 × 1.17 shroud with one fully rounded end, two 9-blade fans
// (r 0.477 at x ±0.736), a bracket with four ports, a PCIe tab.
//
// The files carry no colour and no timing. RigArt.svelte styles them by class:
//   b body · p raised part · v void (wells, slots) · d detail line · m marker
// and moves them, only while the component has load, by class:
//   fan      turns about its own origin; `ccw` turns the other way
//   platter  turns, slower — a disk, not a fan
//   seek     an actuator arm hunting across the platter
//   lit      an overlay that lights in its turn. `--p` (0–1) is its place in
//            the cycle, so a row of them reads as a wave or a chase.
//            `soft` for large faces · `blink` for an LED · `steady` stays on
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const out = join(root, 'panel/src/lib/settings/rig/art');
const range = (a, b, step = 1) => Array.from({ length: Math.ceil((b - a) / step) }, (_, i) => a + i * step);
const lines = (xs, f) => `<path class="d" d="${xs.map(f).join(' ')}"/>`;
const phase = (p) => `style="--p:${+p.toFixed(3)}"`;
const rect = (cls, x, y, w, h, rx = 0, extra = '') => `<rect class="${cls}" x="${x}" y="${y}" width="${w}" height="${h}"${rx ? ` rx="${rx}"` : ''}${extra ? ` ${extra}` : ''}/>`;
// a raised part and the overlay that lights it, in one go
const part = (x, y, w, h, rx, p) => rect('p', x, y, w, h, rx) + rect('lit soft', x, y, w, h, rx, phase(p));

// ---- GPU -------------------------------------------------------------------
const BLADE = 'M-2,-17 C-7,-28 -5,-40 5,-47 C12,-48.5 18,-46 22,-41 C12,-38 7,-29 5,-17.5 Z';
const fan = (cx, cy, ccw) => `
  <g transform="translate(${cx},${cy})">
    <circle class="v" r="52.2"/><circle class="d" r="47.5"/>
    <g class="fan${ccw ? ' ccw' : ''}">${range(0, 9).map((i) => `<path class="p" d="${BLADE}" transform="rotate(${i * 40})"/>`).join('')}</g>
    <circle class="b" r="16.8"/><circle class="d" r="5"/>
  </g>`;
const gpu = {
  viewBox: '-1 0 272 153',
  body: `
  <path class="b" d="M5,2 H209.7 A58.5,58.5 0 0 1 209.7,119 H5 Z"/>
  <path class="b" d="M5,119 H108 V131.5 H5 Z"/>
  ${lines(range(12, 106, 4), (x) => `M${x},123 V131.5`)}
  <path class="b" d="M2,2 H5 V151 H2 Z"/>
  ${[10.3, 36.9, 63.2, 89.6].map((y) => rect('b', 0.5, y, 6, 19.5, 1)).join('')}
  <path class="d" d="M124,2 V119 M148,2 V119"/>${fan(62.6, 60.5, true)}${fan(209.8, 60.5, false)}`,
};

// ---- CPU: substrate, contact pads, heat spreader with chamfered corners ------
// Under load the pads light in a chase around the package: work going round.
const at = range(0, 11).map((i) => 22 + i * 10.6);
const ring = [
  ...at.map((p) => [p, 9, 5, 7]), ...at.map((p) => [134, p, 7, 5]),
  ...[...at].reverse().map((p) => [p, 134, 5, 7]), ...[...at].reverse().map((p) => [9, p, 7, 5]),
];
const cpu = {
  viewBox: '0 0 150 150',
  body: `
  ${rect('b', 3, 3, 144, 144, 7)}
  ${ring.map(([x, y, w, h], k) => rect('d', x, y, w, h) + rect('lit', x, y, w, h, 0, phase(k / ring.length))).join('')}
  <path class="p" d="M33,24 H117 L126,33 V117 L117,126 H33 L24,117 V33 Z"/>
  <path class="d" d="M38,52 H92 M38,62 H78 M38,72 H86"/>
  <circle class="d" cx="112" cy="112" r="3.5"/>
  <path class="m" d="M6,134 L16,144 H6 Z"/>`,
};

// ---- RAM: one DIMM — eight packages, SPD chip, keyed edge, latch notches -----
// Under load a wave crosses the eight packages.
const ram = {
  viewBox: '0 0 270 78',
  body: `
  <path class="b" d="M4,4 H266 V26 a5,5 0 0 0 0,10 V74 H138 V66 H132 V74 H4 V36 a5,5 0 0 0 0,-10 Z"/>
  ${[14, 43, 72, 101, 147, 176, 205, 234].map((x, i) => part(x, 16, 24, 30, 1.5, i / 8)).join('')}
  ${rect('p', 129, 24, 12, 14, 1)}
  <path class="d" d="M4,60 H266"/>
  ${lines([...range(10, 128, 4), ...range(142, 264, 4)], (x) => `M${x},63 V72`)}`,
};

// ---- NVMe: M.2 2280 — keyed edge connector, controller, DRAM, two NAND -------
// Under load the data path lights in order: controller, cache, flash, flash.
const nvme = {
  viewBox: '0 0 270 74',
  body: `
  <path class="b" d="M4,4 H266 V28 a9,9 0 0 0 0,18 V70 H4 V52 H15 V44 H4 Z"/>
  <path class="d" d="M20,4 V70"/>
  ${lines([...range(9, 42, 4.5), ...range(57, 68, 4.5)], (y) => `M4,${y} H15`)}
  ${part(36, 16, 42, 42, 2, 0)}
  <path class="d" d="M44,26 H70 M44,32 H62"/>
  ${part(90, 24, 22, 26, 1.5, 0.18)}${part(126, 14, 52, 46, 2, 0.36)}${part(188, 14, 52, 46, 2, 0.54)}`,
};

// ---- SATA SSD: 2.5" case — L-keyed data + power connector, label, screws -----
// No moving parts: an activity LED, and a level strip that fills along the label.
const sata = {
  viewBox: '0 0 220 154',
  body: `
  ${rect('b', 4, 4, 212, 146, 8)}
  ${[[16, 16], [204, 16], [16, 138], [204, 138]].map(([x, y]) => `<circle class="d" cx="${x}" cy="${y}" r="3.2"/>`).join('')}
  ${rect('v', 4, 30, 9, 30, 1)}${rect('v', 4, 68, 9, 56, 1)}
  ${lines([...range(35, 58, 4.5), ...range(73, 121, 4.5)], (y) => `M6,${y} H11`)}
  ${rect('p', 34, 28, 156, 98, 4)}
  <path class="d" d="M46,46 H128 M46,56 H104 M46,100 H92"/>
  ${range(0, 6).map((i) => rect('d', 46 + i * 13, 108, 9, 6, 1) + rect('lit', 46 + i * 13, 108, 9, 6, 1, phase(i / 6))).join('')}
  <circle class="d" cx="172" cy="44" r="3"/><circle class="lit blink" cx="172" cy="44" r="3"/>`,
};

// ---- HDD: 3.5" drive with the lid off — platter, spindle, actuator -----------
// The one drive you can see working: the platter turns and the arm hunts.
// Pivot (176,112); the arm is 78 long, so rotate(35°–58°) sweeps the head from
// the hub to the outer tracks. At rest it parks at 40°.
const hdd = {
  viewBox: '0 0 220 154',
  body: `
  ${rect('b', 4, 4, 212, 146, 7)}
  ${[[14, 14], [206, 14], [14, 140], [206, 140], [110, 10], [110, 144]].map(([x, y]) => `<circle class="d" cx="${x}" cy="${y}" r="2.8"/>`).join('')}
  <g transform="translate(88,77)">
    <circle class="p" r="63"/>
    <g class="platter"><path class="d" d="M0,-56 A56,56 0 0 1 48.5,-28 M-39.6,39.6 A56,56 0 0 1 -56,0 M0,44 A44,44 0 0 1 -31.1,31.1 M38.1,22 A44,44 0 0 1 22,38.1 M-27.7,-16 A32,32 0 0 1 0,-32"/></g>
    <circle class="d" r="50"/><circle class="b" r="21"/><circle class="d" r="8"/>
  </g>
  <path class="p" d="M148,86 H200 V136 H166 L148,118 Z"/>
  <g transform="translate(176,112)">
    <g class="seek" transform="rotate(40)">
      <path class="b" d="M0,-8 L-64,-3 H-78 V3 H-64 L0,8 L14,10 A16,16 0 0 0 14,-10 Z"/>
      <path class="d" d="M-72,-3 V3 M-12,-4 H-50 M-12,4 H-50"/>
    </g>
    <circle class="b" r="6.5"/><circle class="d" r="2.4"/>
  </g>`,
};

// ---- NAS: two-bay tower, after the site's nas model --------------------------
// Power LED steady, the two drive LEDs blinking out of step.
const led = (y, cls, p = 0) => `<circle class="p" cx="98" cy="${y}" r="2.2"/><circle class="lit ${cls}" cx="98" cy="${y}" r="2.2" ${phase(p)}/>`;
const nas = {
  viewBox: '0 0 124 170',
  body: `
  ${rect('b', 14, 160, 16, 6, 1)}${rect('b', 94, 160, 16, 6, 1)}
  ${rect('b', 4, 4, 116, 156, 9)}
  ${rect('p', 12, 12, 30, 140, 3)}${rect('p', 46, 12, 30, 140, 3)}
  <path class="d" d="M17,20 V38 M51,20 V38 M82,10 V154"/>
  ${led(22, 'steady')}${led(31, 'blink', 0)}${led(40, 'blink', 0.37)}
  ${rect('v', 90, 70, 16, 3, 1)}${rect('v', 89, 88, 18, 7, 1)}${rect('v', 92, 104, 12, 5, 2.5)}
  <circle class="p" cx="98" cy="136" r="7"/><circle class="d" cx="98" cy="136" r="2.6"/>`,
};

const ART = { gpu, cpu, ram, nvme, sata, hdd, nas };
const svg = ({ viewBox, body }) =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${viewBox}" fill="none" stroke-width="1" stroke-linejoin="round" stroke-linecap="round">${body.replace(/\n\s*/g, '')}</svg>\n`;

mkdirSync(out, { recursive: true });
rmSync(join(out, 'ssd.svg'), { force: true }); // renamed nvme.svg once SATA and HDD joined it
for (const [name, art] of Object.entries(ART)) writeFileSync(join(out, `${name}.svg`), svg(art));
console.log(`wrote ${Object.keys(ART).length} illustrations to ${out}`);

// ---- preview: the same files and the SAME css, outside the app ---------------
// The styles are lifted out of RigArt.svelte rather than copied, so what this
// page shows is what the panel does.
if (process.argv.includes('--preview')) {
  const component = readFileSync(join(out, 'RigArt.svelte'), 'utf8');
  const artCss = component.match(/<style>([\s\S]*?)<\/style>/)[1].replace(/:global\(([^)]*)\)/g, '$1');
  const css = `
  :root{--bg:#161616;--s1:#1C1C1C;--s2:#242424;--s3:#2B2B2B;--line:#2E2E2E;--line2:#3D3D3D;--text:#FAFAFA;--muted:#A1A1AA;--dim:#71717A;--faint:#52525B;--accent:#E54866;--accent-line:rgba(229,72,102,.42);--ok:#4bb894;--info:#5aa0d6;--semantic:#b48ad6}
  body{margin:0;padding:40px;background:var(--bg);color:var(--text);font:13px/1.5 system-ui,sans-serif}
  h1{font-size:15px;font-weight:600;margin:0 0 4px} h2{font-size:9.5px;letter-spacing:.12em;text-transform:uppercase;color:var(--dim);font-weight:500;margin:36px 0 14px}
  p{color:var(--muted);margin:0;max-width:74ch} .row{display:flex;gap:28px;flex-wrap:wrap;align-items:flex-end} figure{margin:0} figcaption{font:9.5px ui-monospace,monospace;color:var(--dim);margin-top:8px;max-width:230px;line-height:1.5}
  @media (prefers-reduced-motion:reduce){.rig-art *{animation:none!important}}
  .card{width:236px;background:var(--s1);border-radius:9px;box-shadow:0 0 0 1px var(--line2);padding:12px 14px 12px}
  .card .art{display:flex;justify-content:center;padding:6px 0 10px} .name{font-weight:600;font-size:12.5px} .sub{font:9.5px ui-monospace,monospace;color:var(--dim)}
  .bar{display:flex;height:6px;border-radius:2px;overflow:hidden;background:var(--s3);margin:10px 0 6px} .bar i{display:block;height:100%}
  .legend{display:flex;gap:10px;font:9.5px ui-monospace,monospace;color:var(--muted);flex-wrap:wrap} .legend b{display:inline-block;width:6px;height:6px;border-radius:1px;margin-right:4px}`;
  // the component's own formula: five speed steps, 2.52 s barely loaded, 0.6 s flat out
  const dur = (load) => `${(3 - 2.4 * (Math.ceil(Math.min(1, load) * 5) / 5)).toFixed(2)}s`;
  const art = (k, w, tone = 'idle', load = 0) => `<span class="rig-art ${tone}${load > 0 ? ' live' : ''}" style="--w:${w}px;--dur:${dur(load)}">${svg(ART[k])}</span>`;
  const fig = (k, w, cap, tone, load) => `<figure>${art(k, w, tone, load)}<figcaption>${cap}</figcaption></figure>`;
  const W = { gpu: 230, cpu: 120, ram: 230, nvme: 230, sata: 170, hdd: 170, nas: 100 };
  const MOVES = {
    gpu: 'fans counter-rotate', cpu: 'pads chase round the package', ram: 'a wave crosses the packages',
    nvme: 'controller → cache → flash → flash', sata: 'LED + level strip', hdd: 'platter turns, arm hunts', nas: 'power steady, drive LEDs blink',
  };
  const all = (tone, load, cap) => Object.keys(ART).map((k) => fig(k, W[k], cap(k), tone, load)).join('');
  const gpuCard = (n, name, vram, segs, tone, load) => `<div class="card"><div class="art">${art('gpu', 196, tone, load)}</div>
    <div class="name">GPU ${n} · ${name}</div><div class="sub">${vram}</div>
    <div class="bar">${segs.map(([c, p]) => `<i style="width:${p}%;background:${c}"></i>`).join('')}</div>
    <div class="legend">${segs.map(([c, p, l]) => `<span><b style="background:${c}"></b>${l}</span>`).join('')}</div></div>`;
  const html = `<!doctype html><meta charset="utf-8"><title>Rig art preview</title><style>${css}${artCss}</style>
  <h1>Rig management — component illustrations</h1>
  <p>Generated by scripts/make-rig-art.mjs from the same SVG files the panel inlines, styled by the CSS lifted out of RigArt.svelte. Flat fill, hairline edges — the language cerveau-site uses for its models. Colour comes only from the panel's tokens. One number drives every animation: <b>load</b>, 0 to 1. At 0 nothing moves.</p>
  <h2>The set · idle — load 0, nothing moves</h2><div class="row">${all('idle', 0, (k) => `${k}.svg`)}</div>
  <h2>In use · load 0.4</h2><div class="row">${all('on', 0.4, (k) => `${k} — ${MOVES[k]}`)}</div>
  <h2>Flat out · load 1.0 — same motion, faster</h2><div class="row">${all('on', 1, (k) => k)}</div>
  <h2>Draft · the accent means "the layout being edited targets this", nothing else</h2><div class="row">${all('accent', 0.4, (k) => k)}</div>
  <h2>As SvelteFlow node cards · a TP=2 layout on cards 2 and 3, embedder on card 4</h2><div class="row">
  ${gpuCard(0, 'RTX 3090', '24 GB · PCIe 4.0 ×16 · 250 W', [['var(--faint)', 2, 'other 0.3']], 'idle', 0)}
  ${gpuCard(2, 'RTX 3090', '24 GB · PCIe 4.0 ×16 · 250 W', [['var(--accent)', 66, 'weights 15.8'], ['var(--info)', 22, 'KV 5.3'], ['var(--faint)', 2, 'other 0.3']], 'on', 0.67)}
  ${gpuCard(4, 'RTX 3060', '12 GB · PCIe 4.0 ×16 · 190 W', [['var(--semantic)', 13, 'embedder 1.5'], ['var(--faint)', 28, 'desktop 3.4']], 'on', 0.17)}</div>`;
  // docs-private is git-ignored: a page to look at, not something to ship
  const page = join(root, 'docs-private/rig-art-preview.html');
  mkdirSync(dirname(page), { recursive: true });
  writeFileSync(page, html);
  console.log(`wrote ${page}`);
}
