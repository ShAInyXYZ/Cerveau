"""Versioned Nemotron wire conventions, independent of inference dependencies."""
import json
from pathlib import Path

MODEL_V2 = "nemotron-embed-v2"
DOCUMENT_CONVENTION = "nemotron3-passage-v1"
QUERY_CONVENTION = "nemotron3-query-v1"
DIMENSIONS = 2048


def prepare_inputs(texts, input_type):
    """Prefix raw input exactly once; input text itself is never reinterpreted."""
    if input_type not in ("query", "document"):
        raise ValueError("input_type must be query or document")
    prefix = "query: " if input_type == "query" else "passage: "
    return [prefix + text for text in texts]


def validate_model_convention(model_dir):
    """Fail closed for a substituted checkpoint with incompatible prompt/pooling metadata."""
    root = Path(model_dir)
    prompts = json.loads((root / "config_sentence_transformers.json").read_text())
    pooling = json.loads((root / "1_Pooling" / "config.json").read_text())
    if (
        prompts.get("prompts", {}).get("query") != "query: "
        or prompts.get("prompts", {}).get("document") != "passage: "
        or prompts.get("default_prompt_name") is not None
        or pooling.get("word_embedding_dimension") != DIMENSIONS
        or pooling.get("pooling_mode_mean_tokens") is not True
        or pooling.get("include_prompt") is not True
        or any(
            value is True
            for key, value in pooling.items()
            if key.startswith("pooling_mode_") and key != "pooling_mode_mean_tokens"
        )
    ):
        raise ValueError("model metadata does not match the Nemotron passage/query convention")
