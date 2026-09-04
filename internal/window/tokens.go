package window

import (
	"bytes"
	"cerveau/internal/llm"
	"context"
	"crypto/sha1"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Counter interface {
	Count(ctx context.Context, text string) int
}

type HTTPCounter struct {
	base  string
	http  *http.Client
	mu    sync.RWMutex
	cache map[[20]byte]int
}

func NewHTTPCounter(modelBase string) *HTTPCounter {
	return &HTTPCounter{
		base:  modelBase,
		http:  &http.Client{Timeout: 10 * time.Second, Transport: llm.CoreTransport()},
		cache: map[[20]byte]int{},
	}
}

func (c *HTTPCounter) Count(ctx context.Context, text string) int {
	key := sha1.Sum([]byte(text))
	c.mu.RLock()
	if n, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		return n
	}
	c.mu.RUnlock()

	n := c.fetch(ctx, text)
	c.mu.Lock()
	c.cache[key] = n
	c.mu.Unlock()
	return n
}

func (c *HTTPCounter) fetch(ctx context.Context, text string) int {
	body, _ := json.Marshal(map[string]string{"content": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/tokenize", bytes.NewReader(body))
	if k := strings.TrimSpace(os.Getenv("CRV_MODEL_KEY")); err == nil && k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	if err != nil {
		return estimate(text)
	}
	req.Header.Set("content-type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return estimate(text)
	}
	defer resp.Body.Close()
	var out struct {
		Tokens []int `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Tokens == nil {
		return estimate(text)
	}
	return len(out.Tokens)
}

func estimate(text string) int {
	return (len(text) + 3) / 4
}

// CounterFunc adapts a plain function to Counter (used by tests and by
// callers that already have a tokenizer).
type CounterFunc func(context.Context, string) int

func (f CounterFunc) Count(ctx context.Context, text string) int { return f(ctx, text) }

// ContextProber reports the serving window of the engine behind the endpoint.
// 0 means "could not tell" — the caller keeps whatever it was configured with.
type ContextProber interface {
	MaxContext(ctx context.Context) int
}

// MaxContext asks the Core how big its window actually is, so the budget is
// never a number a human typed into config.json and forgot when the Core
// changed. vLLM publishes max_model_len on /v1/models; llama.cpp publishes
// n_ctx on /props. Both are the SERVING window (what the engine will accept),
// not the model's training length.
func (c *HTTPCounter) MaxContext(ctx context.Context) int {
	if n := c.getInt(ctx, "/v1/models", func(v map[string]any) int {
		data, _ := v["data"].([]any)
		if len(data) == 0 {
			return 0
		}
		m, _ := data[0].(map[string]any)
		f, _ := m["max_model_len"].(float64)
		return int(f)
	}); n > 0 {
		return n
	}
	return c.getInt(ctx, "/props", func(v map[string]any) int {
		s, _ := v["default_generation_settings"].(map[string]any)
		f, _ := s["n_ctx"].(float64)
		return int(f)
	})
}

func (c *HTTPCounter) getInt(ctx context.Context, path string, pick func(map[string]any) int) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0
	}
	if k := strings.TrimSpace(os.Getenv("CRV_MODEL_KEY")); k != "" {
		req.Header.Set("Authorization", "Bearer "+k) // vLLM behind VLLM_API_KEY 401s otherwise
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return 0
	}
	return pick(v)
}
