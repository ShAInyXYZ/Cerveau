package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func embeddingTestSchema(base string) map[string]any {
	return map[string]any{"fields": []any{map[string]any{
		"name": "embedding", "type": "float[]", "num_dim": embeddingDimensions,
		"embed": map[string]any{"from": []string{"content"}, "model_config": map[string]any{"model_name": "openai/nemotron-embed", "api_key": "local", "url": base}},
	}}}
}

func embeddingTestResponse() map[string]any {
	vector := make([]float64, embeddingDimensions)
	vector[0] = 1
	return map[string]any{"model": embeddingModelV2, "embedding_convention": embeddingQueryConvention, "document_convention": embeddingDocumentConvention, "data": []any{map[string]any{"index": 0, "embedding": vector}}}
}

func TestHybridEmbedsTypedQueryWithoutChangingIndexOrLexicalText(t *testing.T) {
	query := "query: world.js TypeError sky"
	vector := make([]float64, embeddingDimensions)
	for i := range vector {
		vector[i] = float64((i%29)-14) / 1234.56789
	}
	encodedVector, err := json.Marshal(vector)
	if err != nil || len(encodedVector) <= 4000 {
		t.Fatalf("fixture must exceed Typesense's URL limit: bytes=%d err=%v", len(encodedVector), err)
	}
	wantVectorQuery := "embedding:(" + string(encodedVector) + ", k:5)"
	var embedded, searched int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/collections/memory":
			if r.Method != http.MethodGet {
				t.Errorf("mutated schema: %s", r.Method)
			}
			json.NewEncoder(w).Encode(embeddingTestSchema(srv.URL))
		case "/v2/embeddings":
			embedded++
			if r.Method != http.MethodPost || r.Header.Get("x-typesense-api-key") != "" {
				t.Errorf("unsafe sidecar request: method=%s leaked-key=%v", r.Method, r.Header.Get("x-typesense-api-key") != "")
			}
			var body struct {
				Input              []string `json:"input"`
				InputType          string   `json:"input_type"`
				Model              string   `json:"model"`
				DocumentConvention string   `json:"document_convention"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Input) != 1 || body.Input[0] != query || body.InputType != "query" || body.Model != embeddingModelV2 || body.DocumentConvention != embeddingDocumentConvention {
				t.Errorf("query changed or mistyped: %+v", body)
			}
			response := embeddingTestResponse()
			response["data"].([]any)[0].(map[string]any)["embedding"] = vector
			json.NewEncoder(w).Encode(response)
		case "/multi_search":
			searched++
			if r.Method != http.MethodPost || r.RequestURI != "/multi_search" || r.Header.Get("x-typesense-api-key") != "secret-test-key" {
				t.Errorf("hybrid transport: method=%s uri-bytes=%d authenticated=%v", r.Method, len(r.RequestURI), r.Header.Get("x-typesense-api-key") == "secret-test-key")
			}
			var body struct {
				Searches []map[string]any `json:"searches"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Searches) != 1 {
				t.Errorf("invalid multi_search body: count=%d err=%v", len(body.Searches), err)
				http.Error(w, "invalid searches", 400)
				return
			}
			params := body.Searches[0]
			if params["collection"] != "memory" || params["q"] != query || params["query_by"] != "content" || params["vector_query"] != wantVectorQuery || params["per_page"] != float64(5) || params["filter_by"] != "memory_type:=episodic && session_id:=`s1`" {
				t.Errorf("hybrid search contract changed: q=%v query_by=%v per_page=%v filter_by=%v", params["q"], params["query_by"], params["per_page"], params["filter_by"])
			}
			json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"hits": []any{map[string]any{"document": Doc{ID: "s1:evt_000001", SessionID: "s1", MemoryType: "episodic", EvtType: "tool.result", EvtID: "evt_000001", Content: "retained evidence"}}}}}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	hits, err := NewTSClient(srv.URL, "secret-test-key").Search(context.Background(), query, "episodic", "", 5, true, "session_id:=`s1`")
	if err != nil || len(hits) != 1 || embedded != 1 || searched != 1 {
		t.Fatalf("query failed: hits=%v err=%v embed=%d searches=%d", hits, err, embedded, searched)
	}
}

func TestHybridMultiSearchRejectsMalformedOrFailedResults(t *testing.T) {
	for name, response := range map[string]string{
		"invalid JSON":     `{"results":`,
		"wrapper error":    `{"error":"bad request","code":400,"results":[{"hits":[]}]}`,
		"wrapper code":     `{"code":503,"results":[{"hits":[]}]}`,
		"missing results":  `{}`,
		"null results":     `{"results":null}`,
		"empty results":    `{"results":[]}`,
		"multiple results": `{"results":[{"hits":[]},{"hits":[]}]}`,
		"result error":     `{"results":[{"error":"unknown field","code":400}]}`,
		"error with hits":  `{"results":[{"error":"failed","hits":[]}]}`,
		"result code":      `{"results":[{"code":503,"hits":[]}]}`,
		"missing hits":     `{"results":[{}]}`,
		"null hits":        `{"results":[{"hits":null}]}`,
		"wrong hits shape": `{"results":[{"hits":{}}]}`,
		"missing document": `{"results":[{"hits":[{}]}]}`,
		"null document":    `{"results":[{"hits":[{"document":null}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/collections/memory":
					json.NewEncoder(w).Encode(embeddingTestSchema(srv.URL))
				case "/v2/embeddings":
					json.NewEncoder(w).Encode(embeddingTestResponse())
				case "/multi_search":
					fmt.Fprint(w, response)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			if _, err := NewTSClient(srv.URL, "k").Search(context.Background(), "fixture", "", "", 5, true, ""); err == nil {
				t.Fatal("accepted failed or malformed multi_search response")
			}
		})
	}
}

func TestHybridMultiSearchFailureFallsBackToLexical(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var hybridCalls, lexicalCalls int
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/collections/memory":
					json.NewEncoder(w).Encode(embeddingTestSchema(srv.URL))
				case "/v2/embeddings":
					json.NewEncoder(w).Encode(embeddingTestResponse())
				case "/multi_search":
					hybridCalls++
					w.WriteHeader(status)
					fmt.Fprint(w, `{"results":[{"code":400,"error":"vector search unavailable"}]}`)
				case "/collections/memory/documents/search":
					lexicalCalls++
					if r.Method != http.MethodGet || r.URL.Query().Get("vector_query") != "" || r.URL.Query().Get("q") != "fixture failure" || r.URL.Query().Get("filter_by") != "memory_type:=episodic && session_id:=`s1`" {
						t.Error("fallback changed lexical query, scope, or transport")
					}
					json.NewEncoder(w).Encode(map[string]any{"hits": []any{map[string]any{"document": Doc{ID: "s1:evt_000001", SessionID: "s1", MemoryType: "episodic", EvtType: "error", EvtID: "evt_000001", Content: "fixture failure"}}}})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			result := NewRecall(NewTSClient(srv.URL, "k"), t.TempDir(), true).OnErrorWithStatus(context.Background(), "s1", "fixture failure", nil)
			if result.Backend != "lexical" || !result.Degraded || len(result.Pulls) != 1 || hybridCalls != 1 || lexicalCalls != 1 {
				t.Fatalf("fallback result=%+v hybrid=%d lexical=%d", result, hybridCalls, lexicalCalls)
			}
		})
	}
}

func TestHybridRejectsUnverifiedDocumentConvention(t *testing.T) {
	for _, variation := range []string{"model", "dimensions", "indexing_prefix", "from", "url"} {
		t.Run(variation, func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/collections/memory" {
					t.Errorf("used incompatible embedding backend: %s", r.URL.Path)
					http.Error(w, "unexpected", 500)
					return
				}
				schema := embeddingTestSchema(srv.URL)
				field := schema["fields"].([]any)[0].(map[string]any)
				embed := field["embed"].(map[string]any)
				model := embed["model_config"].(map[string]any)
				switch variation {
				case "model":
					model["model_name"] = "different-model"
				case "dimensions":
					field["num_dim"] = 1024
				case "indexing_prefix":
					model["indexing_prefix"] = "passage:"
				case "from":
					embed["from"] = []string{"title", "content"}
				case "url":
					model["url"] = "file:///private/model"
				}
				json.NewEncoder(w).Encode(schema)
			}))
			defer srv.Close()
			if _, err := NewTSClient(srv.URL, "k").queryVector(context.Background(), "fixture"); err == nil {
				t.Fatal("accepted incompatible collection")
			}
		})
	}
}

func TestHybridRejectsWrongQueryConventionOrVector(t *testing.T) {
	for _, variation := range []string{"model", "query_convention", "document_convention", "dimensions", "zero", "index", "count"} {
		t.Run(variation, func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/collections/memory" {
					json.NewEncoder(w).Encode(embeddingTestSchema(srv.URL))
					return
				}
				response := embeddingTestResponse()
				entry := response["data"].([]any)[0].(map[string]any)
				switch variation {
				case "model":
					response["model"] = "nemotron-embed"
				case "query_convention":
					response["embedding_convention"] = embeddingDocumentConvention
				case "document_convention":
					response["document_convention"] = "unknown"
				case "dimensions":
					entry["embedding"] = []float64{1}
				case "zero":
					entry["embedding"] = make([]float64, embeddingDimensions)
				case "index":
					entry["index"] = 1
				case "count":
					response["data"] = []any{entry, entry}
				}
				json.NewEncoder(w).Encode(response)
			}))
			defer srv.Close()
			if _, err := NewTSClient(srv.URL, "k").queryVector(context.Background(), "fixture"); err == nil {
				t.Fatal("accepted incompatible query vector")
			}
		})
	}
}

func TestOldSidecarFallsBackWithoutLegacyQueryEmbedding(t *testing.T) {
	var v2Calls, lexicalCalls int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/collections/memory":
			json.NewEncoder(w).Encode(embeddingTestSchema(srv.URL))
		case "/v2/embeddings":
			v2Calls++
			http.NotFound(w, r)
		case "/collections/memory/documents/search":
			lexicalCalls++
			if r.URL.Query().Get("vector_query") != "" || r.URL.Query().Get("query_by") != "content" {
				t.Error("legacy fallback was not lexical")
			}
			json.NewEncoder(w).Encode(map[string]any{"hits": []any{map[string]any{"document": Doc{ID: "s1:evt_000001", SessionID: "s1", MemoryType: "episodic", EvtType: "error", EvtID: "evt_000001", Content: "fixture failure"}}}})
		default:
			t.Errorf("unsafe legacy query route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	result := NewRecall(NewTSClient(srv.URL, "k"), t.TempDir(), true).OnErrorWithStatus(context.Background(), "s1", "fixture failure", nil)
	if result.Backend != "lexical" || !result.Degraded || len(result.Pulls) != 1 || v2Calls != 1 || lexicalCalls != 1 {
		t.Fatalf("fallback result=%+v v2=%d lexical=%d", result, v2Calls, lexicalCalls)
	}
}
