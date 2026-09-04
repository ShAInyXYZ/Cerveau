# Engine patches

The TP=4 profiles run a **patched** vLLM 0.27.1 from `~/qwen-serving/venv`.
The patches live in `~/qwen-serving/patches/` and are applied to the installed
package, not to a source checkout.

> **Re-apply every patch after any `pip install`, upgrade or venv rebuild.**
> Nothing in the venv records that they were applied. The checks below are the
> only way to know.

## The one that gates a setting

**`vllm-pr48375-mamba-prefix-eagle.patch`** — required for `PREFIX_CACHE=1`
on any Qwen3.8 Core.

Qwen3.8-27B is a hybrid: 48 of its 64 layers are Gated DeltaNet linear
attention carrying recurrent state, and the checkpoint ships an MTP draft head.
`MambaManager.find_longest_cache_hit` ignored its `drop_eagle_block` argument,
so a cached page whose recurrent-state snapshot had been taken over draft
tokens that verification later **rejected** could still be reused. Generation
then resumes from a state that never existed.

The failure is silent. No exception, no crash: just wrong tool calls and
degraded long-context output. It cannot be seen in a log, only in bad results.

The fix is nine lines in one file — lower the cache-hit search ceiling by one
page. Upstream is [vllm#48375](https://github.com/vllm-project/vllm/pull/48375),
open at the time of writing.

**If this patch is not applied, set `PREFIX_CACHE=0`** in the panel
(Settings → Engine → Profile) or in `~/.config/cerveau/cores.d/<id>.env`.
Correct-but-slower beats fast-and-silently-wrong.

## Checking

```bash
cd "$(~/qwen-serving/venv/bin/python -c 'import vllm,os;print(os.path.dirname(vllm.__file__))')"
patch -p2 --dry-run --forward < ~/qwen-serving/patches/vllm-pr48375-mamba-prefix-eagle.patch
```

`Reversed (or previously applied) patch detected!` means it is **applied**.
A clean dry-run means it is **missing** — apply it, or turn prefix caching off.

Same shape for the others, adjusting `-p`; each patch header carries its own
apply line.

## Proving prefix caching is safe

Greedy decoding on a long shared prefix must give byte-identical output whether
the prefix was cached or not. Divergence across runs is the corruption showing.

```bash
# 5 identical greedy requests over a ~1500-token shared prefix
# expected: 1 distinct output. More than 1 = the patch is missing or broken.
```

Measured 2026-09-04 with the patch applied, W8A16 TP=4: 5/5 byte-identical,
4/4 long-context recall across cache-hit turns, 6 concurrent requests correct,
no crash signatures. Six-turn growing agent history prefilled in 20.8 s against
52.0 s with prefix caching off.

## The other patches

| Patch | What it does |
|---|---|
| `vllm-pr48375-mamba-prefix-eagle.patch` | **Gates `PREFIX_CACHE=1`.** See above. |
| `vllm-pr50021-gdn-spec-bounds.patch` | Bounds accepted-token state lookups in the GDN/Mamba spec-decode kernels. Without it, several concurrent MTP requests raise `cudaErrorIllegalAddress`. With it, `--max-num-seqs 8` is fine; the model card's advice to cap at 2 no longer applies. Its header notes it deliberately skipped the prefix-caching hunks — 48375 is that missing piece. |
| `marlin-int8-layer-select.patch` | Layer selection for the marlin int8 path. |
| `marlin-int8-negative-scales.patch` | Negative-scale handling in the same kernels. |
| `qwen3_5-embed-quant.patch` | Embedding quantisation for this architecture. |
| `qwen3_5-mtp-draft-vocab.patch` | Draft-head vocabulary alignment. |
| `spec-decode-attn.patch` | Split-KV attention for spec-decode batches on FLASH_ATTN (Ampere). Enabled by `VLLM_SPEC_DECODE_ATTN=1`, which the launcher sets only when `KV=bf16`. |
| `sampler-small-topk-fast-softmax.patch` | Faster softmax for small top-k. |

## Settings that are not patch-dependent

Recorded here because they get re-suggested from the model card, and are
already handled:

- **`--mamba-cache-mode align`** — vLLM sets it automatically whenever prefix
  caching is on for this architecture. Do not pass it.
- **`--reasoning-parser qwen3`** — already in `Gpu-Rig/quad/start_qwen_tp4.sh`.
  Without it the whole thinking block lands in `message.content`.
- **`KV=fp8`** — already means `fp8_e4m3` on CUDA. Not a different setting.
- **`MARLIN_INT8` must stay `0`.** It does *not* select the W8A16 marlin path,
  which is already the default. It forces int8 **activations** (W8A8), and the
  kernel refuses them on this checkpoint's int8-weight `lm_head`/`mtp`.
- **P2P is unavailable on this rig.** Every GPU pair reports no peer access and
  the topology is `NODE` across the host bridge, so `NCCL_P2P_LEVEL=NVL` is
  correct and there is no peer-access win to unlock by dropping to TP=2.
