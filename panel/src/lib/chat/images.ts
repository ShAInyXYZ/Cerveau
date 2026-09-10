export interface ImageAttachment {
  data_url: string; name?: string; width?: number; height?: number; bytes?: number; sha256?: string;
}
export const MAX_IMAGE_BYTES = 256 * 1024;
// Journals may have been imported outside validated ingress. Never turn an
// old remote URL or SVG into an automatic browser request/render.
export function isInlineImage(value: unknown): value is string {
  return typeof value === 'string' && value.length <= Math.ceil(MAX_IMAGE_BYTES / 3) * 4 + 32
    && /^data:image\/(?:png|jpeg);base64,[A-Za-z0-9+/]+={0,2}$/.test(value);
}
export function fittedSize(width: number, height: number, max = 1280) {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width < 1 || height < 1) throw new Error('Invalid image dimensions');
  const ratio = Math.min(1, max / width, max / height);
  return { width: Math.max(1, Math.round(width * ratio)), height: Math.max(1, Math.round(height * ratio)) };
}
export function boundedCrop(x: number, y: number, width: number, height: number, sourceWidth: number, sourceHeight: number) {
  if (![x,y,width,height,sourceWidth,sourceHeight].every(Number.isFinite) || width < 1 || height < 1) throw new Error('Crop must have positive dimensions');
  x = Math.max(0, Math.min(sourceWidth - 1, Math.round(x)));
  y = Math.max(0, Math.min(sourceHeight - 1, Math.round(y)));
  return { x, y, width: Math.min(Math.round(width), sourceWidth - x), height: Math.min(Math.round(height), sourceHeight - y) };
}
export function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => { const img = new Image(); img.onload = () => resolve(img); img.onerror = () => reject(new Error('Could not decode this image')); img.src = url; });
}
export async function compressImage(source: CanvasImageSource, width: number, height: number, name: string, crop = { x: 0, y: 0, width, height }): Promise<ImageAttachment> {
  const rect = boundedCrop(crop.x, crop.y, crop.width, crop.height, width, height);
  let size = fittedSize(rect.width, rect.height);
  const canvas = document.createElement('canvas');
  for (let attempt = 0; attempt < 5; attempt++) {
    canvas.width = size.width; canvas.height = size.height;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('Image processing is unavailable');
    ctx.fillStyle = '#ffffff'; ctx.fillRect(0, 0, size.width, size.height);
    ctx.drawImage(source, rect.x, rect.y, rect.width, rect.height, 0, 0, size.width, size.height);
    for (const quality of [0.82, 0.65, 0.45]) {
      const data_url = canvas.toDataURL('image/jpeg', quality);
      const encoded = data_url.split(',')[1];
      const bytes = atob(encoded).length;
      if (bytes <= MAX_IMAGE_BYTES) return { data_url, name, bytes, ...size };
    }
    size = fittedSize(size.width, size.height, Math.floor(Math.max(size.width, size.height) * 0.75));
  }
  throw new Error('Image could not fit the 256 KiB limit');
}
