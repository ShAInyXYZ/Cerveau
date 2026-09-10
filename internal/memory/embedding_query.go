package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
)

const (
	embeddingModelV2            = "nemotron-embed-v2"
	embeddingQueryConvention    = "nemotron3-query-v1"
	embeddingDocumentConvention = "nemotron3-passage-v1"
	embeddingDimensions         = 2048
	embeddingResponseBytes      = 2 << 20
)

// Existing memory vectors were generated from "passage: " + content. Generate
// an explicitly typed query vector against that unchanged space instead of
// asking the legacy OpenAI-compatible route to treat a query as a passage.
// Every lookup verifies the collection contract; no schema migration or remote
// reindex is needed, and an old sidecar cannot silently accept the new route.
func (c *TSClient) queryVector(ctx context.Context, query string) ([]float64, error) {
	response, err := c.do(ctx, http.MethodGet, "/collections/memory", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding schema lookup: HTTP %d", response.StatusCode)
	}
	var collection struct {
		Fields []struct {
			Name       string `json:"name"`
			Type       string `json:"type"`
			Dimensions int    `json:"num_dim"`
			Embed      struct {
				From  []string `json:"from"`
				Model struct {
					Name           string `json:"model_name"`
					URL            string `json:"url"`
					IndexingPrefix string `json:"indexing_prefix"`
				} `json:"model_config"`
			} `json:"embed"`
		} `json:"fields"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, embeddingResponseBytes)).Decode(&collection); err != nil {
		return nil, fmt.Errorf("embedding schema decode: %w", err)
	}
	base := ""
	for _, field := range collection.Fields {
		if field.Name != "embedding" {
			continue
		}
		model := field.Embed.Model
		if field.Type != "float[]" || field.Dimensions != embeddingDimensions || len(field.Embed.From) != 1 || field.Embed.From[0] != "content" || (model.Name != "openai/nemotron-embed" && model.Name != "nemotron-embed") || model.IndexingPrefix != "" {
			return nil, fmt.Errorf("unsupported embedding document convention; keep existing vectors and verify migration before hybrid search")
		}
		base = strings.TrimRight(model.URL, "/")
		break
	}
	endpoint, err := url.Parse(base)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("embedding schema has no supported sidecar URL")
	}
	body, err := json.Marshal(map[string]any{"input": []string{query}, "input_type": "query", "model": embeddingModelV2, "document_convention": embeddingDocumentConvention})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v2/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Do not forward the Typesense API key to a separate service.
	embedded, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer embedded.Body.Close()
	if embedded.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query embedding unavailable: HTTP %d", embedded.StatusCode)
	}
	var result struct {
		Model              string `json:"model"`
		Convention         string `json:"embedding_convention"`
		DocumentConvention string `json:"document_convention"`
		Data               []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(embedded.Body, embeddingResponseBytes)).Decode(&result); err != nil {
		return nil, fmt.Errorf("query embedding decode: %w", err)
	}
	if result.Model != embeddingModelV2 || result.Convention != embeddingQueryConvention || result.DocumentConvention != embeddingDocumentConvention || len(result.Data) != 1 || result.Data[0].Index != 0 || len(result.Data[0].Embedding) != embeddingDimensions {
		return nil, fmt.Errorf("query embedding convention or dimensions mismatch")
	}
	var norm float64
	for _, value := range result.Data[0].Embedding {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("query embedding contains non-finite values")
		}
		norm += value * value
	}
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, fmt.Errorf("query embedding has invalid norm")
	}
	return result.Data[0].Embedding, nil
}
