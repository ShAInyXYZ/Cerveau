// Brand marks for the things that run on the machine — NVIDIA for a Nemotron
// embedder, Docker for a container found on a card, Typesense, Cerveau itself.
//
// Nothing here names a brand. The marks, the words that identify each one and
// its colour are all in brands.json, written by scripts/vendor-brand-icons.mjs
// (the usual brand-icon resolution, run at development time so the panel never goes
// to the network for a picture). A module hands over its own strings — title,
// model, systemd unit, process name — and gets back whichever brand they name,
// or null: the caller then draws a neutral icon.
import registry from './brands.json';

const svgs = import.meta.glob('./*.svg', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;
const pngs = import.meta.glob('./*.png', { query: '?url', import: 'default', eager: true }) as Record<string, string>;

interface Entry { match: string[]; color?: string; own?: boolean; raster?: boolean }
export interface Brand {
  id: string;
  /** the brand's colour, made legible on a near-black card; undefined = none known */
  color?: string;
  /** inline svg — tinted by the card unless `own` */
  svg?: string;
  /** a raster mark, for a brand that publishes no vector one */
  url?: string;
  /** drawn in the brand's own colours: do not tint it */
  own: boolean;
}

/** The first brand named by any of a module's own strings. Order in
 *  brands.json is precedence: a systemd unit called docker-… is Docker even
 *  though the process inside it is python3. */
export function brandFor(...names: (string | null | undefined)[]): Brand | null {
  const hay = names.filter(Boolean).join(' \n ').toLowerCase();
  if (!hay) return null;
  for (const [id, e] of Object.entries(registry as Record<string, Entry>)) {
    if (!e.match.some((w) => hay.includes(w.toLowerCase()))) continue;
    return { id, color: legible(e.color), svg: svgs[`./${id}.svg`], url: pngs[`./${id}.png`], own: !!e.own || !!e.raster };
  }
  return null;
}

/** A brand colour chosen for a white page can vanish on #161616. Lift it
 *  toward white until it can be read; a CSS variable is already ours. */
export function legible(color: string | undefined): string | undefined {
  if (!color || !/^#[0-9a-f]{6}$/i.test(color)) return color;
  let [r, g, b] = [1, 3, 5].map((i) => parseInt(color.slice(i, i + 2), 16));
  const lum = () => (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
  for (let i = 0; i < 12 && lum() < 0.3; i++) [r, g, b] = [r, g, b].map((v) => Math.round(v + (255 - v) * 0.12));
  return `#${[r, g, b].map((v) => v.toString(16).padStart(2, '0')).join('')}`;
}
