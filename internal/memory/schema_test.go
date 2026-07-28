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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"hits": []any{}})
	}))
	defer srv.Close()
	c := NewTSClient(srv.URL, "k")
	c.Search(context.Background(), "test", "episodic", "", 5, true, "")
	if !strings.Contains(lastQuery, "query_by=content%2Cembedding") && !strings.Contains(lastQuery, "query_by=content,embedding") {
		t.Fatalf("hybrid query_by missing: %s", lastQuery)
	}
	c.Search(context.Background(), "test", "episodic", "", 5, false, "")
	if strings.Contains(lastQuery, "embedding") {
		t.Fatalf("keyword query should not include embedding: %s", lastQuery)
	}
}
