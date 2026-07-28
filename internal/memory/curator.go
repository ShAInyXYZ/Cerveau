package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	SemanticMergeThreshold = 0.9
	SemanticLinkThreshold  = 0.7
)

type Candidate struct {
	Content      string   `json:"content"`
	Category     string   `json:"category"`
	SessionID    string   `json:"session_id,omitempty"` // originating session, so memories can be filtered by it
	SourceEvtIDs []string `json:"source_evt_ids"`
	Supersedes   string   `json:"supersedes,omitempty"`
}

type WriteResult struct {
	Action string `json:"action"`
	DocID  string `json:"doc_id"`
}

type Curator struct {
	client      *TSClient
	pendingPath string
	healthy     func() bool
	mu          sync.Mutex
}

func NewCurator(client *TSClient, pendingPath string, healthy func() bool) *Curator {
	return &Curator{client: client, pendingPath: pendingPath, healthy: healthy}
}

func (c *Curator) Write(ctx context.Context, cand Candidate) (WriteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cand.Category == "" {
		cand.Category = "fact"
	}
	if cand.Content = strings.TrimSpace(cand.Content); cand.Content == "" {
		return WriteResult{}, fmt.Errorf("empty content")
	}
	if c.healthy != nil && !c.healthy() {
		return WriteResult{Action: "queued"}, c.enqueue(cand)
	}
	return c.writeNow(ctx, cand)
}

func (c *Curator) writeNow(ctx context.Context, cand Candidate) (WriteResult, error) {
	if cand.Supersedes != "" {
		return c.supersede(ctx, cand)
	}
	match, sim, err := c.dedup(ctx, cand.Content)
	if err != nil {
		return WriteResult{}, err
	}
	now := time.Now().Unix()
	switch {
	case match != nil && sim >= SemanticMergeThreshold:
		match.Confidence = minF(match.Confidence+0.1, 1.0)
		match.LastSeen = now
		match.Sources = union(match.Sources, cand.SourceEvtIDs)
		if err := c.client.Upsert(ctx, *match); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{Action: "merged", DocID: match.ID}, nil
	case match != nil && sim >= SemanticLinkThreshold:
		doc := c.newDoc(cand, now)
		doc.RelatedTo = []string{match.ID}
		doc.Review = true
		if err := c.client.Upsert(ctx, *doc); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{Action: "linked", DocID: doc.ID}, nil
	default:
		doc := c.newDoc(cand, now)
		if err := c.client.Upsert(ctx, *doc); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{Action: "created", DocID: doc.ID}, nil
	}
}

func (c *Curator) newDoc(cand Candidate, now int64) *Doc {
	return &Doc{
		ID:         fmt.Sprintf("sem_%d", time.Now().UnixNano()),
		SessionID:  cand.SessionID,
		MemoryType: "semantic",
		EvtType:    "",
		EvtID:      "",
		Content:    cand.Content,
		TS:         now,
		Category:   cand.Category,
		Confidence: 0.7,
		Sources:    cand.SourceEvtIDs,
		LastSeen:   now,
	}
}

func (c *Curator) supersede(ctx context.Context, cand Candidate) (WriteResult, error) {
	doc := c.newDoc(cand, time.Now().Unix())
	doc.Confidence = 0.9
	if err := c.client.Upsert(ctx, *doc); err != nil {
		return WriteResult{}, err
	}
	old, err := c.client.Get(ctx, cand.Supersedes)
	if err == nil {
		old.Superseded = true
		old.SupersededBy = doc.ID
		c.client.Upsert(ctx, *old)
	}
	return WriteResult{Action: "superseded", DocID: doc.ID}, nil
}

func (c *Curator) dedup(ctx context.Context, content string) (*Doc, float64, error) {
	hits, err := c.client.Search(ctx, content, "semantic", "", 3, false, "superseded:=false")
	if err != nil {
		return nil, 0, err
	}
	if len(hits) == 0 {
		return nil, 0, nil
	}
	best := &hits[0].Doc
	return best, jaccard(content, best.Content), nil
}

func jaccard(a, b string) float64 {
	wa := wordSet(a)
	wb := wordSet(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	unionN := len(wa) + len(wb) - inter
	return float64(inter) / float64(unionN)
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,;:!?()[]{}\"'")
		if w != "" {
			out[w] = true
		}
	}
	return out
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func (c *Curator) enqueue(cand Candidate) error {
	if err := os.MkdirAll(filepath.Dir(c.pendingPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(c.pendingPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(cand)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func (c *Curator) DrainPending(ctx context.Context) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.healthy != nil && !c.healthy() {
		return 0, nil
	}
	data, err := os.ReadFile(c.pendingPath)
	if err != nil {
		return 0, nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	remaining := []string{}
	drained := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		var cand Candidate
		if err := json.Unmarshal([]byte(line), &cand); err != nil {
			continue
		}
		if _, err := c.writeNow(ctx, cand); err != nil {
			remaining = append(remaining, line)
			continue
		}
		drained++
	}
	if drained > 0 {
		out := strings.Join(remaining, "\n")
		if out != "" {
			out += "\n"
		}
		os.WriteFile(c.pendingPath, []byte(out), 0o644)
	}
	return drained, nil
}
