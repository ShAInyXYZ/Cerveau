#!/usr/bin/env python3
"""
Nemotron-3-Embed-1B sidecar — OpenAI-compatible /v1/embeddings on :8081.

cerveau's Typesense calls this to embed memory content (server-side remote
embedding). Not called by the Go core directly.

Run:
    python3 sidecars/nemotron_embed.py
    # or: uvicorn nemotron_embed:app --host 127.0.0.1 --port 8081

Model dir defaults to ~/.crv/models/Nemotron-3-Embed-1B (override with EMBED_MODEL).
Passages are prefixed 'passage: ' per the model's sentence-transformers config;
queries should be prefixed 'query: ' by the caller (Typesense sends raw content
as passages, which is correct for indexing).
"""
import os

# CPU BY DEFAULT. The GPU is fully committed to the vLLM Core (0.90 util), and
# an embedder sharing it OOM'd on any realistic batch: a single short string
# returned in 15ms while a 16-chunk batch of code 500'd outright — a failure
# that looks like "memory never retrieves" rather than an error.
#
# MEASURED on 32 threads, 16 code chunks (~40 lines each):
#   bf16,  8 threads -> 277 ms/chunk   <- chosen
#   fp32, 16 threads -> 516 ms/chunk   (fp32 is SLOWER: the bandwidth saved by
#                                       bf16 beats its lack of a native CPU path)
# Embedding runs on memory writes and retrievals, a few calls per turn — not
# per token — so ~0.3s lands inside a 30s+ turn unnoticed. Set EMBED_DEVICE=cuda
# to go back to the GPU.
EMBED_DEVICE = os.environ.get("EMBED_DEVICE", "cpu")
EMBED_THREADS = int(os.environ.get("EMBED_THREADS", "8"))

if EMBED_DEVICE == "cpu":
    # Hide the GPU before torch initialises, so no CUDA context is created at
    # all — a context alone costs a few hundred MiB the Core now needs.
    os.environ["CUDA_VISIBLE_DEVICES"] = ""

import torch
from fastapi import FastAPI
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer

MODEL_DIR = os.environ.get(
    "EMBED_MODEL", os.path.expanduser("~/.crv/models/Nemotron-3-Embed-1B")
)

app = FastAPI()
_model = None


def model():
    global _model
    if _model is None:
        if EMBED_DEVICE == "cpu":
            # More threads is not better: 16 beat 8 on fp32 but 32 was worse
            # than both — past ~8 the per-batch sync cost outweighs the split.
            torch.set_num_threads(EMBED_THREADS)
        _model = SentenceTransformer(
            MODEL_DIR, trust_remote_code=True, device=EMBED_DEVICE
        )
    return _model


class EmbedRequest(BaseModel):
    input: list[str] | str
    model: str | None = None


@app.get("/health")
def health():
    return {
        "ok": True,
        "model": MODEL_DIR,
        "device": EMBED_DEVICE,
        "threads": EMBED_THREADS if EMBED_DEVICE == "cpu" else None,
    }


# Typesense (and OpenAI clients) probe /v1/models to validate the model exists
# before embedding. Report the name Typesense is configured with.
@app.get("/v1/models")
def models():
    return {
        "object": "list",
        "data": [
            {"id": "openai/nemotron-embed", "object": "model", "owned_by": "local"},
            {"id": "nemotron-embed", "object": "model", "owned_by": "local"},
        ],
    }


@app.post("/v1/embeddings")
def embeddings(req: EmbedRequest):
    texts = [req.input] if isinstance(req.input, str) else req.input
    # index-time content = passages; the model was trained with these prefixes
    prefixed = ["passage: " + t for t in texts]
    vecs = model().encode(prefixed, normalize_embeddings=True)
    data = [
        {"object": "embedding", "index": i, "embedding": v.tolist()}
        for i, v in enumerate(vecs)
    ]
    return {
        "object": "list",
        "data": data,
        "model": req.model or "nemotron-embed",
        "usage": {"prompt_tokens": 0, "total_tokens": 0},
    }


if __name__ == "__main__":
    import uvicorn

    # warm the model so the first real request isn't slow
    model()
    uvicorn.run(app, host="127.0.0.1", port=8081)
