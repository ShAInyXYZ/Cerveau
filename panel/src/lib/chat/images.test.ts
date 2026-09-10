import { describe, it, expect } from 'vitest';
import { fittedSize, boundedCrop, isInlineImage } from './images';
describe('bounded image geometry', () => {
  it('never renders remote, SVG or oversized imported image sources', () => {
    expect(isInlineImage('data:image/png;base64,YWJj')).toBe(true);
    for (const source of ['https://example.invalid/track', 'data:image/svg+xml;base64,YWJj', 'data:image/jpeg;base64,'+'A'.repeat(400000), null]) expect(isInlineImage(source)).toBe(false);
  });
  it('fits large portrait and landscape images without upscaling', () => {
    expect(fittedSize(3840, 2160)).toEqual({ width: 1280, height: 720 });
    expect(fittedSize(2160, 3840)).toEqual({ width: 720, height: 1280 });
    expect(fittedSize(80, 40)).toEqual({ width: 80, height: 40 });
  });
  it('clips regions to the captured source', () => {
    expect(boundedCrop(90, 20, 90, 90, 100, 100)).toEqual({ x: 90, y: 20, width: 10, height: 80 });
    expect(() => boundedCrop(0, 0, 0, 4, 100, 100)).toThrow();
    expect(() => fittedSize(Infinity, 100)).toThrow();
  });
});
