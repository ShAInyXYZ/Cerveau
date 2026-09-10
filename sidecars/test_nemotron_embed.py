"""Contract regressions with inference/framework dependencies stubbed; no model load."""
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest.mock import patch

from embedding_conventions import (
    DOCUMENT_CONVENTION,
    MODEL_V2,
    QUERY_CONVENTION,
    prepare_inputs,
    validate_model_convention,
)


class StubApp:
    def __init__(self):
        self.routes = {}

    def get(self, path):
        return self.post(path)

    def post(self, path):
        def register(fn):
            self.routes[path] = fn
            return fn
        return register


class StubHTTPException(Exception):
    def __init__(self, status_code, detail):
        self.status_code = status_code
        self.detail = detail


class FakeVector:
    def __init__(self, value):
        self.value = value

    def tolist(self):
        return [self.value]


class FakeModel:
    def __init__(self):
        self.calls = []

    def encode(self, texts, **kwargs):
        self.calls.append((list(texts), kwargs))
        return [FakeVector(float(len(text))) for text in texts]


def write_metadata(root):
    prompts = {
        "prompts": {"query": "query: ", "document": "passage: "},
        "default_prompt_name": None,
    }
    pooling = {
        "word_embedding_dimension": 2048,
        "pooling_mode_mean_tokens": True,
        "pooling_mode_cls_token": False,
        "include_prompt": True,
    }
    (root / "config_sentence_transformers.json").write_text(json.dumps(prompts))
    (root / "1_Pooling").mkdir(exist_ok=True)
    (root / "1_Pooling" / "config.json").write_text(json.dumps(pooling))


class EmbeddingConventionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        dependencies = {
            "torch": types.SimpleNamespace(set_num_threads=lambda count: None),
            "fastapi": types.SimpleNamespace(FastAPI=StubApp, HTTPException=StubHTTPException),
            "pydantic": types.SimpleNamespace(BaseModel=object),
            "sentence_transformers": types.SimpleNamespace(SentenceTransformer=None),
        }
        spec = importlib.util.spec_from_file_location(
            "tested_nemotron_embed", Path(__file__).with_name("nemotron_embed.py")
        )
        cls.sidecar = importlib.util.module_from_spec(spec)
        with patch.dict(sys.modules, dependencies), patch.dict("os.environ"):
            spec.loader.exec_module(cls.sidecar)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        write_metadata(self.root)
        self.model = FakeModel()
        self.sidecar._model = self.model
        self.sidecar.MODEL_DIR = str(self.root)

    def request(self, text, input_type="query"):
        return types.SimpleNamespace(
            input=text, input_type=input_type, model=MODEL_V2,
            document_convention=DOCUMENT_CONVENTION,
        )

    def test_explicit_query_prefix_is_distinct_from_document_prefix(self):
        result = self.sidecar.typed_embeddings(self.request(["world.js sky", "query: literal"]))
        self.assertEqual(self.model.calls, [
            (["query: world.js sky", "query: query: literal"], {"normalize_embeddings": True})
        ])
        self.assertEqual(result["embedding_convention"], QUERY_CONVENTION)
        self.assertEqual(result["document_convention"], DOCUMENT_CONVENTION)
        self.assertEqual(result["model"], MODEL_V2)
        self.assertEqual([item["index"] for item in result["data"]], [0, 1])

    def test_document_vectors_keep_identical_legacy_encoder_inputs(self):
        texts = ["world.js sky", "query: literal", "passage: literal", ""]
        legacy = self.sidecar.embeddings(types.SimpleNamespace(input=texts, model="nemotron-embed"))
        typed = self.sidecar.typed_embeddings(self.request(texts, "document"))
        self.assertEqual(self.model.calls[0], self.model.calls[1])
        self.assertEqual(legacy["data"], typed["data"])
        self.assertEqual(self.model.calls[0][0], ["passage: " + text for text in texts])
        self.assertEqual(legacy["model"], "nemotron-embed")
        self.assertEqual(typed["embedding_convention"], DOCUMENT_CONVENTION)

    def test_legacy_model_ids_and_unprefixed_route_remain_unchanged(self):
        for model_id in (None, "nemotron-embed", "openai/nemotron-embed"):
            result = self.sidecar.embeddings(types.SimpleNamespace(input="query: raw", model=model_id))
            self.assertEqual(self.model.calls[-1], (["passage: query: raw"], {"normalize_embeddings": True}))
            self.assertEqual(result["model"], model_id or "nemotron-embed")
        self.assertIn("/v2/embeddings", self.sidecar.app.routes)
        self.assertEqual(
            [item["id"] for item in self.sidecar.models()["data"]],
            ["openai/nemotron-embed", "nemotron-embed"],
        )

    def test_invalid_checkpoint_metadata_refuses_query_before_encode(self):
        path = self.root / "config_sentence_transformers.json"
        metadata = json.loads(path.read_text())
        metadata["default_prompt_name"] = "document"
        path.write_text(json.dumps(metadata))
        with self.assertRaises(StubHTTPException) as raised:
            self.sidecar.typed_embeddings(self.request("sky"))
        self.assertEqual(raised.exception.status_code, 503)
        self.assertEqual(self.model.calls, [])

    def test_metadata_checks_dimensions_prompts_and_pooling(self):
        for filename, key, value in (
            ("config_sentence_transformers.json", "prompts", {"query": "passage: ", "document": "passage: "}),
            ("1_Pooling/config.json", "word_embedding_dimension", 1024),
            ("1_Pooling/config.json", "pooling_mode_mean_tokens", False),
            ("1_Pooling/config.json", "pooling_mode_cls_token", True),
            ("1_Pooling/config.json", "include_prompt", False),
        ):
            with self.subTest(filename=filename, key=key):
                write_metadata(self.root)
                path = self.root / filename
                metadata = json.loads(path.read_text())
                metadata[key] = value
                path.write_text(json.dumps(metadata))
                with self.assertRaises(ValueError):
                    validate_model_convention(self.root)

    def test_unsupported_input_type_is_not_guessed_from_text(self):
        with self.assertRaises(ValueError):
            prepare_inputs(["query: text"], "auto")


if __name__ == "__main__":
    unittest.main()
