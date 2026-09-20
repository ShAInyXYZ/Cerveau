import { describe, it, expect } from 'vitest';
import { brandFor, legible } from './index';

describe('brandFor', () => {
  // the strings are the modules' own, as /api/rig and /api/health report them
  it('names a module from its own strings', () => {
    expect(brandFor('Embedder', 'nemotron-embed')?.id).toBe('nvidia');
    expect(brandFor('Typesense', 'memory · v27.1')?.id).toBe('typesense');
    expect(brandFor('Cerveau', 'crv 0.6.0-alpha')?.id).toBe('cerveau');
    expect(brandFor('vLLM')?.id).toBe('vllm');
    expect(brandFor('llama.cpp')?.id).toBe('llamacpp');
  });

  it('prefers the unit over the process inside it', () => {
    expect(brandFor('docker 0123456789ab', 'python3', 'docker-0123456789ab.scope')?.id).toBe('docker');
    expect(brandFor('python3')?.id).toBe('python');
  });

  it('returns null for something it does not know — the card draws a neutral icon', () => {
    expect(brandFor('blender', 'blender-bin')).toBeNull();
    expect(brandFor()).toBeNull();
    expect(brandFor('', undefined, null)).toBeNull();
  });

  it('carries a mark and a colour for every brand in the registry', () => {
    const nvidia = brandFor('nvidia')!;
    expect(nvidia.svg).toContain('<svg');
    expect(nvidia.color).toMatch(/^#/);
    expect(nvidia.own).toBe(false);
    // Typesense publishes no vector mark: its own raster icon, never tinted
    const ts = brandFor('typesense')!;
    expect(ts.svg).toBeUndefined();
    expect(ts.url).toBeTruthy();
    expect(ts.own).toBe(true);
    // Cerveau's colour is the panel's accent, not a hex of its own
    expect(brandFor('cerveau')!.color).toBe('var(--accent)');
  });
});

describe('legible', () => {
  it('lifts a colour that would vanish on a near-black card, and leaves the rest alone', () => {
    expect(legible('#74B71B')).toBe('#74b71b');
    const lifted = legible('#1035bc')!;   // Typesense's wordmark blue
    const lum = (h: string) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16)).reduce((a, v, i) => a + v * [0.2126, 0.7152, 0.0722][i], 0) / 255;
    expect(lum(lifted)).toBeGreaterThanOrEqual(0.3);
    expect(legible('var(--accent)')).toBe('var(--accent)');
    expect(legible(undefined)).toBeUndefined();
  });
});
