package window

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"net/http"
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
		http:  &http.Client{Timeout: 10 * time.Second},
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
