package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TSClient struct {
	base string
	key  string
	http *http.Client
}

func NewTSClient(base, key string) *TSClient {
	return &TSClient{base: strings.TrimRight(base, "/"), key: key, http: &http.Client{Timeout: 10 * time.Second}}
}

type Doc struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"session_id"`
	MemoryType string   `json:"memory_type"`
	EvtType    string   `json:"evt_type"`
	EvtID      string   `json:"evt_id"`
	Content    string   `json:"content"`
	TS         int64    `json:"ts"`
	Category   string   `json:"category,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	Superseded bool     `json:"superseded"`
	SupersededBy string `json:"superseded_by,omitempty"`
	RelatedTo  []string `json:"related_to,omitempty"`
	LastSeen   int64    `json:"last_seen,omitempty"`
	Review     bool     `json:"review,omitempty"`
}

var semanticFields = []map[string]any{
	{"name": "category", "type": "string", "facet": true, "optional": true},
	{"name": "confidence", "type": "float", "optional": true},
	{"name": "sources", "type": "string[]", "optional": true},
	{"name": "superseded", "type": "bool", "facet": true, "optional": true},
	{"name": "superseded_by", "type": "string", "optional": true},
	{"name": "related_to", "type": "string[]", "optional": true},
	{"name": "last_seen", "type": "int64", "optional": true},
	{"name": "review", "type": "bool", "facet": true, "optional": true},
}

func (c *TSClient) EnsureSchema(ctx context.Context, embedURL string) error {
	fields := []map[string]any{
		{"name": "session_id", "type": "string", "facet": true},
		{"name": "memory_type", "type": "string", "facet": true},
		{"name": "evt_type", "type": "string", "facet": true},
		{"name": "evt_id", "type": "string"},
		{"name": "content", "type": "string"},
		{"name": "ts", "type": "int64"},
	}
	fields = append(fields, semanticFields...)
	if embedURL != "" {
		fields = append(fields, map[string]any{
			"name": "embedding",
			"type": "float[]",
			"embed": map[string]any{
				"from": []string{"content"},
				"model_config": map[string]any{
					"model_name": "openai/nemotron-embed",
					"api_key":    "local",
					"url":        embedURL,
				},
			},
		})
	}
	schema := map[string]any{"name": "memory", "fields": fields}
	resp, err := c.do(ctx, http.MethodPost, "/collections", schema)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		return nil
	}
	if resp.StatusCode != 409 && !strings.Contains(string(body), "already exists") {
		return fmt.Errorf("ensure schema: %s", body)
	}
	return c.ensureFields(ctx, fields)
}

func (c *TSClient) ensureFields(ctx context.Context, want []map[string]any) error {
	resp, err := c.do(ctx, http.MethodGet, "/collections/memory", nil)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("fetch collection: %s", body)
	}
	var col struct {
		Fields []map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(body, &col); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, f := range col.Fields {
		if name, _ := f["name"].(string); name != "" {
			have[name] = true
		}
	}
	missing := []map[string]any{}
	for _, f := range want {
		name, _ := f["name"].(string)
		if name != "" && !have[name] {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	patch := map[string]any{"fields": missing}
	resp2, err := c.do(ctx, http.MethodPatch, "/collections/memory", patch)
	if err != nil {
		return err
	}
	pbody, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode >= 300 {
		return fmt.Errorf("add fields: %s", pbody)
	}
	return nil
}

func (c *TSClient) Upsert(ctx context.Context, doc Doc) error {
	resp, err := c.do(ctx, http.MethodPost, "/collections/memory/documents?action=upsert", doc)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upsert %s: %s", doc.ID, body)
	}
	return nil
}

type Hit struct {
	Doc   Doc
	Score float64
}

func (c *TSClient) Search(ctx context.Context, q, memoryType, sessionID string, limit int, hybrid bool, extraFilter string) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	params := url.Values{}
	params.Set("q", q)
	if hybrid {
		params.Set("query_by", "content,embedding")
	} else {
		params.Set("query_by", "content")
	}
	params.Set("per_page", fmt.Sprint(limit))
	filters := []string{}
	if memoryType != "" {
		filters = append(filters, "memory_type:="+memoryType)
	}
	if sessionID != "" {
		filters = append(filters, "session_id:="+sessionID)
	}
	if extraFilter != "" {
		filters = append(filters, extraFilter)
	}
	if len(filters) > 0 {
		params.Set("filter_by", strings.Join(filters, " && "))
	}
	resp, err := c.do(ctx, http.MethodGet, "/collections/memory/documents/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search: %s", body)
	}
	var out struct {
		Hits []struct {
			Document Doc `json:"document"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(out.Hits))
	for _, h := range out.Hits {
		hits = append(hits, Hit{Doc: h.Document})
	}
	return hits, nil
}

func (c *TSClient) Get(ctx context.Context, id string) (*Doc, error) {
	resp, err := c.do(ctx, http.MethodGet, "/collections/memory/documents/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("get %s: status %d", id, resp.StatusCode)
	}
	var d Doc
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

// BackfillSessionIDs repairs semantic docs written before session stamping: it
// derives the session from the doc's sources (e.g. "20260727-101048-essay:evt_5"
// -> "20260727-101048-essay") and upserts it. One-time, idempotent — docs that
// already have a session_id are skipped. Returns the number repaired.
func (c *TSClient) BackfillSessionIDs(ctx context.Context) (int, error) {
	hits, err := c.Search(ctx, "*", "semantic", "", 250, false, "") // Typesense caps per_page at 250
	if err != nil {
		return 0, err
	}
	fixed := 0
	for _, h := range hits {
		d := h.Doc
		if d.SessionID != "" || len(d.Sources) == 0 {
			continue
		}
		sid := d.Sources[0]
		if i := strings.Index(sid, ":"); i >= 0 {
			sid = sid[:i] // strip any :evt_id suffix
		}
		if sid == "" {
			continue
		}
		d.SessionID = sid
		if err := c.Upsert(ctx, d); err == nil {
			fixed++
		}
	}
	return fixed, nil
}

// DeleteBySession removes all memory docs for a session. If memoryType is set
// (e.g. "episodic"), only that type is deleted — so a caller can drop the noisy
// episodic memories while keeping the distilled semantic summaries. Returns the
// number deleted.
func (c *TSClient) DeleteBySession(ctx context.Context, sessionID, memoryType string) (int, error) {
	if sessionID == "" {
		return 0, fmt.Errorf("session id required")
	}
	filter := "session_id:=" + sessionID
	if memoryType != "" {
		filter += " && memory_type:=" + memoryType
	}
	resp, err := c.do(ctx, http.MethodDelete,
		"/collections/memory/documents?filter_by="+url.QueryEscape(filter), nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("delete: status %d", resp.StatusCode)
	}
	var out struct {
		NumDeleted int `json:"num_deleted"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.NumDeleted, nil
}

func (c *TSClient) do(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	if c.key != "" {
		req.Header.Set("x-typesense-api-key", c.key)
	}
	return c.http.Do(req)
}
