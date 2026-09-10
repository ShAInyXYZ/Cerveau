# Memory schema retries and compatible query embeddings

## Verified model contract

The configured local `Nemotron-3-Embed-1B` checkpoint declares `query: ` for
queries, `passage: ` for documents, no default prompt, mean pooling with the
prompt included, and 2048 output dimensions. Its normalization module and the
sidecar's `normalize_embeddings=True` produce normalized vectors. NVIDIA's
[model card](https://huggingface.co/nvidia/Nemotron-3-Embed-1B-BF16) specifies the
same distinct retrieval prefixes.

The previous sidecar prepended `passage: ` to every `/v1/embeddings` request.
This produced the intended stored document representation but also embedded
Typesense's raw retrieval query as a passage. Prefixing a query at the caller
would have produced `passage: query: ...`, so that alone was not a fix.

## Corrected query path, unchanged stored vectors

`/v1/embeddings`, `nemotron-embed`, and `openai/nemotron-embed` retain their
existing behavior. Every raw document still receives exactly `passage: ` and
the same encoder call. The collection field and schema model ID stay unchanged.

`/v2/embeddings` requires explicit `input_type: query` or `document` and uses
model ID `nemotron-embed-v2`. Its response identifies both conventions:

- Stored documents: `nemotron3-passage-v1`.
- Retrieval queries: `nemotron3-query-v1`.

The new route validates the checkpoint's prompt/pooling metadata before
encoding. It prefixes a query with `query: ` exactly once. Input is always raw
text: a literal `query: ` or `passage: ` within that text is not stripped or
reinterpreted. Its document branch has identical encoder inputs and options to
the old route, as asserted by the sidecar contract tests.

Before each hybrid lookup, the Go client reads the collection schema and checks
that `embedding` is a 2048-dimensional float array from `content`, uses a known
legacy Nemotron model ID, and has no additional indexing prefix. It then calls
the versioned query route and validates the returned convention, dimensions,
index, and finite nonzero vector. The Typesense API key is not sent to the
sidecar. The query is passed to Typesense as an explicit `vector_query` alongside
the unchanged lexical `q` and `query_by=content`; this is Typesense's documented
[hybrid search interface](https://typesense.org/docs/latest/api/vector-search.html#hybrid-search).

There is no collection update, vector rewrite, cursor reset, or reindex in this
fix. Existing stored vectors remain usable. Unsupported model/prefix/dimension
configurations refuse hybrid search instead of guessing their vector space.
Failure-triggered recall falls back to lexical search and then local evidence.
An old sidecar returns 404 on the new route, which follows the same fallback;
the client never sends query text to the old passage-only route.

## Deployment procedure

This work did not restart services, replace the host collection, or write host
memory documents. To enable the correction on the existing index:

1. Verify the current collection has the schema contract above using a read-only
   schema request, and confirm the existing model directory and device settings.
2. Deploy `sidecars/nemotron_embed.py` together with
   `sidecars/embedding_conventions.py`. Restart the existing embedder using its
   unchanged model, device, environment, and checkpoint. No schema PATCH is
   required and no embedding field should be dropped or recreated.
3. Check a benign `/v2/embeddings` query returns one 2048-dimensional vector,
   `model=nemotron-embed-v2`, `embedding_convention=nemotron3-query-v1`, and
   `document_convention=nemotron3-passage-v1`. This is inference only, not an
   indexing operation. The legacy endpoint remains available throughout the
   subsequent harness update.
4. Deploy the harness containing the updated memory client and verify one
   read-only hybrid lookup. Preserve the existing collection and cursor files.

Deploying the sidecar first allows an older harness to continue its previous
behavior until the harness update. Rolling back either component does not
require rewriting document vectors. A different checkpoint, pooling setup,
document prefix, source field, or vector dimension would require an explicit
separate migration; this change does not approve or perform one.

## Schema outage behavior and verification

Indexer ticks now retry schema readiness with delays of 2, 4, 8, 16, and at most
30 seconds. Each schema attempt has a 2-second deadline. Until readiness succeeds,
ticks do not upsert documents or advance any cursor, including cursors over
unindexable notes. An upsert failure invalidates readiness, retains the failed
event for replay, and requires schema readiness again before indexing resumes.
Successful readiness clears the backoff. Startup no longer blocks on a schema
network request; the first background tick performs the same guarded check.

Regression commands:

```text
go test -race -timeout=30s ./internal/memory
python3 -m unittest discover -s sidecars -p 'test_*.py'
```

The Python contract tests stub inference and framework dependencies, require
only the standard library, and load no model or GPU context. They verify prefix
distinction, legacy document-input equivalence, versioned responses, and rejected
checkpoint metadata. Go tests exercise real local HTTP exchanges for typed
query vectors, schema validation, unsupported/old sidecars, lexical fallback,
retry timing, outage recovery, cursor preservation, and deadlines. These tests
verify integration behavior; they do not measure semantic retrieval quality.
