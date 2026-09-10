#!/usr/bin/env python3
"""
Nemotron-3-Embed-1B sidecar — OpenAI-compatible /v1/embeddings on :8081.

Typesense calls the legacy route to embed memory documents. The Go memory
client calls the versioned route directly for typed retrieval-query vectors.

Run:
    python3 sidecars/nemotron_embed.py
    # or: uvicorn nemotron_embed:app --host 127.0.0.1 --port 8081

Model dir defaults to ~/.crv/models/Nemotron-3-Embed-1B (override with EMBED_MODEL).
The legacy /v1/embeddings route preserves its document-vector behavior:
every raw input receives 'passage: '. It must not embed retrieval queries.
The versioned /v2/embeddings route takes an explicit input_type and validates
the model metadata before applying 'query: ' or 'passage: '. The Go client uses
query vectors from this route against the unchanged Typesense document index.
"""
import os
from typing import Literal

from embedding_conventions import (
    DOCUMENT_CONVENTION,
    MODEL_V2,
    QUERY_CONVENTION,
    prepare_inputs,
    validate_model_convention,
)

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
from fastapi import FastAPI, HTTPException
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


class TypedEmbedRequest(EmbedRequest):
    input_type: Literal["query", "document"]
    model: Literal["nemotron-embed-v2"] = MODEL_V2
    document_convention: Literal["nemotron3-passage-v1"] = DOCUMENT_CONVENTION


@app.get("/health")
def health():
    return {
        "ok": True,
        "model": MODEL_DIR,
        "device": EMBED_DEVICE,
        "threads": EMBED_THREADS if EMBED_DEVICE == "cpu" else None,
        "query_endpoint": "/v2/embeddings",
        "document_convention": DOCUMENT_CONVENTION,
        "query_convention": QUERY_CONVENTION,
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


@app.post("/v2/embeddings")
def typed_embeddings(req: TypedEmbedRequest):
    try:
        validate_model_convention(MODEL_DIR)
    except (OSError, ValueError) as exc:
        raise HTTPException(status_code=503, detail="embedding convention validation failed") from exc
    texts = [req.input] if isinstance(req.input, str) else req.input
    # The document branch is byte-for-byte compatible with /v1's old prefix
    # and encode call. Only the query branch changes query semantics.
    prefixed = prepare_inputs(texts, req.input_type)
    vecs = model().encode(prefixed, normalize_embeddings=True)
    return {
        "object": "list",
        "data": [
            {"object": "embedding", "index": i, "embedding": v.tolist()}
            for i, v in enumerate(vecs)
        ],
        "model": MODEL_V2,
        "embedding_convention": QUERY_CONVENTION if req.input_type == "query" else DOCUMENT_CONVENTION,
        "document_convention": DOCUMENT_CONVENTION,
        "usage": {"prompt_tokens": 0, "total_tokens": 0},
    }


if __name__ == "__main__":
    import uvicorn

    # warm the model so the first real request isn't slow
    model()
    uvicorn.run(app, host="127.0.0.1", port=8081)
