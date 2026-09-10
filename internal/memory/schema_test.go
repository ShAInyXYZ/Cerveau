package memory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchemaIncludesEmbedField(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/collections" {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &posted)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"name": "memory"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	c := NewTSClient(srv.URL, "k")
	if err := c.EnsureSchema(context.Background(), "http://localhost:8081"); err != nil {
		t.Fatal(err)
	}
	fields, _ := posted["fields"].([]any)
	embed := false
	for _, f := range fields {
		fm, _ := f.(map[string]any)
		if fm["name"] == "embedding" {
			embed = true
			cfg, _ := fm["embed"].(map[string]any)["model_config"].(map[string]any)
			// base URL only — Typesense appends /v1/embeddings itself; adding
			// /v1 here caused a doubled /v1/v1/embeddings 404 (verified live).
			if cfg["url"] != "http://localhost:8081" {
				t.Fatalf("model_config url = %v", cfg["url"])
			}
		}
	}
	if !embed {
		t.Fatalf("no embedding field in schema: %v", posted)
	}
}

func TestSchemaKeywordOnlyWithoutEmbedder(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &posted)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"name": "memory"})
	}))
	defer srv.Close()
	c := NewTSClient(srv.URL, "k")
	if err := c.EnsureSchema(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	fields, _ := posted["fields"].([]any)
	for _, f := range fields {
		if f.(map[string]any)["name"] == "embedding" {
			t.Fatal("embedding field present without embedder")
		}
	}
}

func TestHybridQueryBy(t *testing.T) {
	var lastQuery string
	var hybridSearch map[string]any
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/collections/memory" {
			json.NewEncoder(w).Encode(embeddingTestSchema(srv.URL))
			return
		}
		if r.URL.Path == "/v2/embeddings" {
			json.NewEncoder(w).Encode(embeddingTestResponse())
			return
		}
		if r.URL.Path == "/multi_search" {
			var body struct {
				Searches []map[string]any `json:"searches"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Searches) != 1 {
				t.Errorf("invalid multi_search request: %v", err)
				http.Error(w, "invalid request", 400)
				return
			}
			hybridSearch = body.Searches[0]
			json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"hits": []any{}}}})
			return
		}
		lastQuery = r.URL.RawQuery
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"hits": []any{}})
	}))
	defer srv.Close()
	c := NewTSClient(srv.URL, "k")
	if _, err := c.Search(context.Background(), "test", "episodic", "", 5, true, ""); err != nil {
		t.Fatal(err)
	}
	if hybridSearch["query_by"] != "content" || hybridSearch["vector_query"] == nil || lastQuery != "" {
		t.Fatal("hybrid explicit vector missing from JSON, or leaked into URL")
	}
	if _, err := c.Search(context.Background(), "test", "episodic", "", 5, false, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastQuery, "query_by=content") || strings.Contains(lastQuery, "embedding") {
		t.Fatalf("keyword query should not include embedding: %s", lastQuery)
	}
}
