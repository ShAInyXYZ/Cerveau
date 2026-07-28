package memory

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cerveau/internal/episodic"
)

type Indexer struct {
	client     *TSClient
	sessDir    string
	cursorPath string
	embedURL   string

	mu      sync.Mutex
	cursor  map[string]string
	stop    chan struct{}
	stopped chan struct{}
}

func NewIndexer(client *TSClient, sessionsDir, cursorPath, embedURL string) *Indexer {
	return &Indexer{
		client:     client,
		sessDir:    sessionsDir,
		cursorPath: cursorPath,
		embedURL:   embedURL,
		cursor:     map[string]string{},
		stop:       make(chan struct{}),
		stopped:    make(chan struct{}),
	}
}

func (ix *Indexer) Start(ctx context.Context) {
	ix.loadCursor()
	if err := ix.client.EnsureSchema(ctx, ix.embedURL); err != nil {
		slog.Warn("indexer: schema ensure failed, will retry on ticks", "err", err)
	}
	go ix.loop(ctx)
}

func (ix *Indexer) Stop() {
	close(ix.stop)
	<-ix.stopped
}

func (ix *Indexer) loop(ctx context.Context) {
	defer close(ix.stopped)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	ix.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ix.stop:
			return
		case <-tick.C:
			ix.Tick(ctx)
		}
	}
}

func (ix *Indexer) Tick(ctx context.Context) {
	sessions, err := os.ReadDir(ix.sessDir)
	if err != nil {
		return
	}
	for _, s := range sessions {
		if !s.IsDir() {
			continue
		}
		ix.indexSession(ctx, s.Name())
	}
}

func (ix *Indexer) indexSession(ctx context.Context, sessionID string) {
	// Instant (ephemeral scratch) sessions are never indexed — no long-term memory.
	if isInstantSession(ix.sessDir, sessionID) {
		return
	}
	path := filepath.Join(ix.sessDir, sessionID, "events.jsonl")
	events, err := episodic.Replay(path)
	if err != nil || len(events) == 0 {
		return
	}
	ix.mu.Lock()
	last := ix.cursor[sessionID]
	ix.mu.Unlock()
	start := 0
	if last != "" {
		for i, ev := range events {
			if ev.ID == last {
				start = i + 1
				break
			}
		}
		if start == 0 && len(events) > 0 && events[len(events)-1].ID <= last {
			return
		}
	}
	for _, ev := range events[start:] {
		content := extractContent(ev)
		if content == "" {
			ix.advance(sessionID, ev.ID)
			continue
		}
		doc := Doc{
			ID:         sessionID + ":" + ev.ID,
			SessionID:  sessionID,
			MemoryType: "episodic",
			EvtType:    string(ev.Type),
			EvtID:      ev.ID,
			Content:    content,
			TS:         ev.TS.Unix(),
		}
		if err := ix.client.Upsert(ctx, doc); err != nil {
			slog.Debug("indexer: upsert failed, will retry next tick", "err", err)
			return
		}
		ix.advance(sessionID, ev.ID)
	}
}

func (ix *Indexer) advance(sessionID, evtID string) {
	ix.mu.Lock()
	ix.cursor[sessionID] = evtID
	ix.mu.Unlock()
	ix.saveCursor()
}

func (ix *Indexer) loadCursor() {
	data, err := os.ReadFile(ix.cursorPath)
	if err != nil {
		return
	}
	ix.mu.Lock()
	json.Unmarshal(data, &ix.cursor)
	ix.mu.Unlock()
}

func (ix *Indexer) saveCursor() {
	ix.mu.Lock()
	data, err := json.Marshal(ix.cursor)
	ix.mu.Unlock()
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(ix.cursorPath), 0o755)
	os.WriteFile(ix.cursorPath, data, 0o644)
}

func extractContent(ev episodic.Event) string {
	var p struct {
		Text   string `json:"text"`
		Name   string `json:"name"`
		Output string `json:"output"`
		Detail string `json:"detail"`
		Step   string `json:"step"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return ""
	}
	switch {
	case p.Text != "":
		return p.Text
	case p.Name != "" && p.Output != "":
		return p.Name + ": " + trunc(p.Output, 500)
	case p.Name != "":
		return p.Name
	case p.Detail != "":
		return p.Detail
	case p.Step != "":
		return "step " + p.Step + " " + p.Status
	}
	return ""
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// isInstantSession peeks a session's meta.json for the instant flag — memory
// writes (indexing + promotion) skip these ephemeral scratch sessions.
func isInstantSession(sessDir, sessionID string) bool {
	data, err := os.ReadFile(filepath.Join(sessDir, sessionID, "meta.json"))
	if err != nil {
		return false
	}
	var m struct {
		Instant bool `json:"instant"`
	}
	return json.Unmarshal(data, &m) == nil && m.Instant
}
