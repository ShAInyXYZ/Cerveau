<script lang="ts">
  import { onMount } from 'svelte';
  import { compressImage, loadImage, type ImageAttachment } from './images';
  let { onAttach, onClose }: { onAttach: (image: ImageAttachment) => void; onClose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let picker: HTMLInputElement;
  let preview = $state('');
  let source: HTMLImageElement | null = null;
  let name = '';
  let width = $state(0), height = $state(0);
  let x = $state(0), y = $state(0), cropWidth = $state(0), cropHeight = $state(0);
  let busy = $state(false), error = $state('');
  let active = true;
  let stream: MediaStream | null = null;
  onMount(() => {
    dialog.showModal();
    return () => { active = false; stream?.getTracks().forEach(t => t.stop()); };
  });
  async function setSource(url: string, label: string) {
    const img = await loadImage(url);
    if (img.naturalWidth > 16384 || img.naturalHeight > 16384 || img.naturalWidth * img.naturalHeight > 64 * 1024 * 1024) throw new Error('Source exceeds the 64 megapixel capture limit');
    if (!active) return;
    source = img; name = label; preview = url;
    width = img.naturalWidth; height = img.naturalHeight;
    x = 0; y = 0; cropWidth = width; cropHeight = height;
  }
  async function filePicked(event: Event) {
    const file = (event.target as HTMLInputElement).files?.[0];
    if (!file) return;
    error = ''; busy = true;
    try {
      if (!['image/png','image/jpeg','image/webp'].includes(file.type) || file.size > 20 * 1024 * 1024) throw new Error('Choose a PNG, JPEG or WebP image under 20 MiB');
      const url = await new Promise<string>((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.onerror = () => reject(new Error('Could not read image')); reader.readAsDataURL(file); });
      await setSource(url, file.name);
    } catch (e) { error = String(e); } finally { busy = false; if (picker) picker.value = ''; }
  }
  async function capture() {
    error = ''; busy = true;
    let video: HTMLVideoElement | null = null;
    try {
      if (!navigator.mediaDevices?.getDisplayMedia) throw new Error('Screen capture is unavailable in this browser. Choose an image instead.');
      stream = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: false });
      if (!active) return;
      video = document.createElement('video'); video.srcObject = stream; video.muted = true;
      await video.play();
      if (!video.videoWidth) throw new Error('No frame was received from the selected source');
      if (video.videoWidth * video.videoHeight > 64 * 1024 * 1024 || video.videoWidth > 16384 || video.videoHeight > 16384) throw new Error('Source exceeds the 64 megapixel capture limit');
      const canvas = document.createElement('canvas'); canvas.width = video.videoWidth; canvas.height = video.videoHeight;
      const ctx = canvas.getContext('2d'); if (!ctx) throw new Error('Image processing is unavailable');
      ctx.drawImage(video, 0, 0);
      // A single frame only. Stop sharing before previewing or editing it.
      stream.getTracks().forEach(t => t.stop()); stream = null;
      await setSource(canvas.toDataURL('image/png'), 'Screen capture');
    } catch (e) { if (active) error = e instanceof DOMException && e.name === 'NotAllowedError' ? 'Capture cancelled or permission denied. Nothing was attached.' : String(e); }
    finally { stream?.getTracks().forEach(t => t.stop()); stream = null; if (video) { video.pause(); video.srcObject = null; } busy = false; }
  }
  async function attach() {
    if (!source || busy) return;
    busy = true; error = '';
    try { const image = await compressImage(source, width, height, name, { x, y, width: cropWidth, height: cropHeight }); if (active) { onAttach(image); onClose(); } }
    catch (e) { error = String(e); } finally { busy = false; }
  }
</script>

<dialog bind:this={dialog} oncancel={onClose} aria-labelledby="capture-title">
  <header><h2 id="capture-title">Attach visual context</h2><button onclick={onClose} aria-label="Close attachment preview">Close</button></header>
  <p>Choose an image, or select a window, tab or screen. Capture stops after one frame. Nothing is sent until you send the message.</p>
  <div class="actions"><button disabled={busy} onclick={() => picker.click()}>Choose image</button><button disabled={busy} onclick={capture}>Capture window or screen</button></div>
  <input class="file" bind:this={picker} type="file" accept="image/png,image/jpeg,image/webp" onchange={filePicked} tabindex="-1" aria-label="Choose image file" />
  {#if preview}
    <div class="preview"><img src={preview} alt="Captured source before cropping" /><div class="crop-outline" style:left={`${x/width*100}%`} style:top={`${y/height*100}%`} style:width={`${Math.min(cropWidth,width-x)/width*100}%`} style:height={`${Math.min(cropHeight,height-y)/height*100}%`}></div></div>
    <fieldset disabled={busy}><legend>Crop area · source pixels ({width} × {height})</legend>
      <label>Left<input type="number" min="0" max={width-1} bind:value={x} /></label><label>Top<input type="number" min="0" max={height-1} bind:value={y} /></label>
      <label>Width<input type="number" min="1" max={width} bind:value={cropWidth} /></label><label>Height<input type="number" min="1" max={height} bind:value={cropHeight} /></label>
    </fieldset>
    <p class="note">JPEG · at most 1280 px per side and 256 KiB. Review for secrets or personal information before attaching.</p>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <footer><button onclick={onClose}>Cancel</button><button class="primary" disabled={!preview || busy} onclick={attach}>{busy ? 'Processing…' : 'Attach preview'}</button></footer>
</dialog>

<style>
  dialog { width: min(680px, calc(100vw - 24px)); max-height: calc(100dvh - 32px); overflow: auto; background: var(--s1); color: var(--text); border: 1px solid var(--line2); border-radius: 12px; padding: 20px; box-shadow: var(--elev-2); }
  dialog::backdrop { background: #0009; } header,footer,.actions { display:flex; gap: 8px; align-items: center; } header { justify-content: space-between; } footer { justify-content: flex-end; margin-top:16px; }
  h2 { font-size:16px; margin:0; } p { font-size:13px; line-height:1.5; color:var(--muted); } button,input { font:inherit; color:var(--text); background:var(--s2); border:1px solid var(--line2); border-radius:6px; padding:8px 10px; } button { cursor:pointer; } button:disabled { opacity:.5; cursor:default; } button:focus-visible,input:focus-visible { outline:2px solid var(--accent); outline-offset:2px; } .primary { background:var(--accent); color:var(--on-accent); border-color:var(--accent); }
  .file { display:none; } .preview { position:relative; margin-top:16px; line-height:0; } img { display:block; width:auto; max-width:100%; max-height:45dvh; object-fit:contain; } .preview { width:fit-content; max-width:100%; margin-inline:auto; } .crop-outline { position:absolute; outline:2px solid var(--accent); outline-offset:-2px; pointer-events:none; }
  fieldset { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:8px; margin:16px 0 0; border:1px solid var(--line2); border-radius:6px; } legend,.note,label { font-size:12px; color:var(--muted); } label { display:flex; flex-direction:column; gap:4px; } input { min-width:0; width:100%; } .error { color:var(--err); } @media(max-width:480px) { fieldset { grid-template-columns:repeat(2,minmax(0,1fr)); } .actions { flex-wrap:wrap; } }
</style>
