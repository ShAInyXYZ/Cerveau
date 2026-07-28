// Simple audio-feedback player. Map semantic event names → sound files. Files
// live in src/sounds/ (drop them there, named by meaning). Anything not yet
// present is a silent no-op, so the app works before/without the audio assets.
//
// Usage:  import { play } from './sound.js';  play('send');
//
// To add a sound: put e.g. src/sounds/send.mp3 there and uncomment/point its
// entry below. Vite fingerprints the import; go:embed bundles it into crv.

// Vite glob import — grabs every audio file in src/sounds/ at build time, keyed
// by its base name (without extension). Add a file, it's available by that name.
const modules = import.meta.glob('../sounds/*.{mp3,wav,ogg,m4a}', {
  eager: true,
  query: '?url',
  import: 'default'
});

// build name -> url map (basename without extension)
const SOUNDS = {};
for (const [path, url] of Object.entries(modules)) {
  const name = path.split('/').pop().replace(/\.[^.]+$/, '');
  SOUNDS[name] = url;
}

// a small cache of Audio elements so repeated plays don't re-fetch
const cache = {};

// ---- settings: master volume, mute, per-sound volumes — persisted ----
const LS = 'crv:sound';
function loadCfg() {
  try { return JSON.parse(localStorage.getItem(LS)) ?? {}; } catch { return {}; }
}
function saveCfg() {
  try { localStorage.setItem(LS, JSON.stringify({ muted, volume, per })); } catch {}
}
const cfg = loadCfg();
let muted = cfg.muted ?? false;
let volume = cfg.volume ?? 0.5;
let per = cfg.per ?? {};      // per-sound volume multiplier 0..1 (default 1)

export function setMuted(v) { muted = !!v; saveCfg(); }
export function isMuted() { return muted; }
export function setVolume(v) { volume = Math.max(0, Math.min(1, v)); saveCfg(); }
export function getVolume() { return volume; }
export function setSoundVolume(name, v) { per[name] = Math.max(0, Math.min(1, v)); saveCfg(); }
export function getSoundVolume(name) { return per[name] ?? 1; }

// play a sound by semantic name; silently does nothing if the file isn't present
export function play(name, { force = false } = {}) {
  if (muted && !force) return; // force = preview from settings even while muted
  const url = SOUNDS[name];
  if (!url) return; // not added yet — no-op
  try {
    let a = cache[name];
    if (!a) { a = new Audio(url); cache[name] = a; }
    a.volume = volume * (per[name] ?? 1);
    a.currentTime = 0;
    a.play().catch(() => {}); // ignore autoplay-gesture rejections
  } catch { /* ignore */ }
}

// what sound names are currently available (for debugging / a settings UI)
export function available() { return Object.keys(SOUNDS); }
