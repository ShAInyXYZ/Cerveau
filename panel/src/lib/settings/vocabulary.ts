// How the panel SAYS a profile's parameters — one vocabulary for the two places
// that show them: the Engine section's editable table (the tips) and the Rig
// page's spec plate (the labels and readings).
//
// This is wording, not knowledge of any profile: a key missing from here is
// still shown, under its own name, with its raw value. Add a key when a profile
// starts using one, and both places learn it.

export interface ParamWord {
  /** the short label on the Rig spec plate; absent = not on the plate */
  label?: string;
  /** the value, the way a person would say it */
  say?: (v: string) => string;
  /** the explanation behind the (i) in the Engine table */
  tip: string;
}

const onOff = (v: string) => (v === '1' ? 'on' : 'off');

/** Parameters of a Core's unit. Order here is the order on the spec plate. */
export const PARAMS: Record<string, ParamWord> = {
  CUDA_VISIBLE_DEVICES: { label: 'cards', say: (v) => v.split(',').map((s) => s.trim()).join(' · '), tip: 'GPU indices for the tensor-parallel group.' },
  GPU_UTIL: { label: 'share of each', say: (v) => `${Math.round(Number(v) * 100)}%`, tip: 'Fraction of each GPU vLLM may claim. Rank 0 also carries the API server — leave headroom.' },
  MAX_LEN: { label: 'window', say: (v) => `${Math.round(Number(v) / 1024)}K`, tip: 'Context window in tokens. Must fit the KV pool or vLLM refuses to start.' },
  KV: { label: 'KV cache', tip: 'KV cache dtype. bf16 = exact; fp8 = half the KV bytes, twice the concurrent context, KLD ≈ 0.' },
  VISION: { label: 'vision', say: onOff, tip: '1 loads the vision tower so the Core reads screenshots; 0 drops it (--language-model-only).' },
  DRAFT_TOKENS: { label: 'drafts', say: (v) => (v === '0' ? 'off' : v), tip: 'MTP speculative drafts per step. 0 = off. Lossless either way.' },
  PREFIX_CACHE: { label: 'prefix cache', say: onOff, tip: 'Reuse the KV of a shared prompt prefix across requests.' },
  TP: { tip: 'Tensor-parallel ranks. Head counts must divide by it.' },
  MAX_PIXELS: { tip: 'Image resolution cap before the vision encoder (1638400 = 1280×1280, ~2k tokens per image).' },
  IMAGES_PER_PROMPT: { tip: 'Upper bound on images in one request; sizes the encoder cache.' },
  MAX_SEQS: { tip: 'Concurrent requests.' },
  BATCHED_TOKENS: { tip: 'Prefill chunk. Larger = faster TTFT on long prompts, more activation memory.' },
};

/** Parameters of the embedder's unit, under a Core. */
export const EMBED_PARAMS: Record<string, ParamWord> = {
  EMBED_DEVICE: { tip: 'cpu or cuda. On a card of its own the embedder runs at GPU speed and leaves the Core\'s cards to the Core; on cpu it needs no card at all.' },
  CUDA_VISIBLE_DEVICES: { tip: 'Which GPU the embedder may see (index from nvidia-smi -L). Keep it off the Core\'s cards.' },
  EMBED_THREADS: { tip: 'CPU threads when EMBED_DEVICE=cpu; also the tokeniser threads on GPU.' },
};

/** Keys that are plumbing, not configuration: never shown on the plate. */
export const HIDDEN_PARAMS = new Set(['PORT', 'MODEL', 'CUDA_DEVICE_ORDER']);

/** "vLLM · BF16 · TP=4" beside the vLLM mark is just "BF16 · TP=4". */
export function profileLabel(name: string | undefined, engine: string | undefined): string {
  if (!name) return '';
  const pre = (engine || '').replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return name.replace(new RegExp(`^${pre}\\s*·\\s*`, 'i'), '') || name;
}

/** The last segment of a model path: what a person calls the checkpoint. */
export const modelName = (path: string | undefined) => (path ?? '').replace(/\/+$/, '').split('/').pop() ?? '';
