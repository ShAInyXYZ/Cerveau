package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

	tickMu        sync.Mutex // serializes readiness checks and cursor advancement
	schemaReady   bool
	schemaBackoff time.Duration
	schemaRetryAt time.Time
	now           func() time.Time
}

const (
	schemaAttemptTimeout = 2 * time.Second
	schemaRetryInitial   = 2 * time.Second
	schemaRetryMax       = 30 * time.Second
)

func NewIndexer(client *TSClient, sessionsDir, cursorPath, embedURL string) *Indexer {
	return &Indexer{
		client:     client,
		sessDir:    sessionsDir,
		cursorPath: cursorPath,
		embedURL:   embedURL,
		cursor:     map[string]string{},
		stop:       make(chan struct{}),
		stopped:    make(chan struct{}),
		now:        time.Now,
	}
}

func (ix *Indexer) Start(ctx context.Context) {
	ix.loadCursor()
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
	ix.tickMu.Lock()
	defer ix.tickMu.Unlock()
	if !ix.ensureReady(ctx) {
		return
	}
	sessions, err := os.ReadDir(ix.sessDir)
	if err != nil {
		return
	}
	for _, s := range sessions {
		if ctx.Err() != nil {
			return
		}
		if !s.IsDir() {
			continue
		}
		if err := ix.indexSession(ctx, s.Name()); err != nil {
			ix.schemaUnavailable()
			return
		}
	}
}

func (ix *Indexer) ensureReady(ctx context.Context) bool {
	if ctx.Err() != nil || ix.client == nil {
		return false
	}
	if ix.schemaReady {
		return true
	}
	if ix.now().Before(ix.schemaRetryAt) {
		return false
	}
	attempt, cancel := context.WithTimeout(ctx, schemaAttemptTimeout)
	defer cancel()
	if err := ix.client.EnsureSchema(attempt, ix.embedURL); err != nil {
		ix.schemaUnavailable()
		slog.Warn("indexer: schema unavailable; indexing paused", "retry_after", ix.schemaBackoff, "err", err)
		return false
	}
	ix.schemaReady, ix.schemaBackoff, ix.schemaRetryAt = true, 0, time.Time{}
	return true
}

func (ix *Indexer) schemaUnavailable() {
	ix.schemaReady = false
	if ix.schemaBackoff == 0 {
		ix.schemaBackoff = schemaRetryInitial
	} else {
		ix.schemaBackoff = min(schemaRetryMax, ix.schemaBackoff*2)
	}
	ix.schemaRetryAt = ix.now().Add(ix.schemaBackoff)
}

func (ix *Indexer) indexSession(ctx context.Context, sessionID string) error {
	// Instant (ephemeral scratch) sessions are never indexed — no long-term memory.
	if isInstantSession(ix.sessDir, sessionID) {
		return nil
	}
	path := filepath.Join(ix.sessDir, sessionID, "events.jsonl")
	events, err := episodic.Replay(path)
	if err != nil || len(events) == 0 {
		return nil
	}
	byID := make(map[string]episodic.Event, len(events))
	for _, ev := range events {
		byID[ev.ID] = ev
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
			return nil
		}
	}
	for _, ev := range events[start:] {
		if err := ctx.Err(); err != nil {
			return err
		}
		content := extractContent(ev)
		kind := string(ev.Type)
		var sources []string
		if ev.Type == episodic.Note || ev.Type == episodic.ToolResult {
			if p, ok := errorEvidence(ev, sessionID, byID); ok {
				content, kind, sources = p.Content, p.Kind, []string{p.SourceRef}
			} else if ev.Type == episodic.ToolResult {
				content = ""
			}
		}
		if content == "" {
			ix.advance(sessionID, ev.ID)
			continue
		}
		doc := Doc{
			ID:         sessionID + ":" + ev.ID,
			SessionID:  sessionID,
			MemoryType: "episodic",
			EvtType:    kind,
			EvtID:      ev.ID,
			Content:    content,
			TS:         ev.TS.Unix(),
			Sources:    sources,
		}
		if err := ix.client.Upsert(ctx, doc); err != nil {
			slog.Debug("indexer: upsert failed, will retry next tick", "err", err)
			return err
		}
		ix.advance(sessionID, ev.ID)
	}
	return nil
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
		Kind   string `json:"kind"`
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
	// Typed notes require their own evidence parser. Never index a reasoning,
	// recall receipt, hypothesis or arbitrary note field as a durable fact.
	if ev.Type == episodic.Note && p.Kind != "" {
		return ""
	}
	if recursiveEvidence(p.Name) {
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

// Only declared observation fields are decoded. Excerpts, categories, source
// identities and hypotheses supplied in a note do not override the tool result.
type recoveryObservationPayload struct {
	Kind            string `json:"kind"`
	SessionID       string `json:"session_id"`
	EvidenceEventID string `json:"evidence_event_id"`
	Tool            string `json:"tool"`
	CheckSHA256     string `json:"check_sha256"`
	OutputSHA256    string `json:"output_sha256"`
	SourceSHA256    string `json:"source_sha256"`
	OK              *bool  `json:"ok"`
}

func recursiveEvidence(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "recovery_read" || strings.Contains(name, "recall") || strings.Contains(name, "memory") || strings.Contains(name, "reasoning") || name == "think"
}

func digestValid(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func recoveryEvidence(ev episodic.Event, sessionID string, byID map[string]episodic.Event) (Pull, bool) {
	var o recoveryObservationPayload
	if ev.Type != episodic.Note || len(ev.Payload) > errorEventBytes || json.Unmarshal(ev.Payload, &o) != nil || o.Kind != "recovery_observation" || (o.SessionID != "" && o.SessionID != sessionID) || !validEventID(o.EvidenceEventID) || o.OK == nil || !digestValid(o.CheckSHA256) || !digestValid(o.OutputSHA256) || recursiveEvidence(o.Tool) {
		return Pull{}, false
	}
	source, ok := byID[o.EvidenceEventID]
	if !ok || source.Type != episodic.ToolResult || source.ID == ev.ID || len(source.Payload) > errorEventBytes {
		return Pull{}, false
	}
	var result struct {
		Name, Output string
		OK           *bool
		SessionID    string `json:"session_id"`
	}
	if json.Unmarshal(source.Payload, &result) != nil || result.Name == "" || result.Name != o.Tool || result.OK == nil || *result.OK != *o.OK || (result.SessionID != "" && result.SessionID != sessionID) || fmt.Sprintf("%x", sha256.Sum256([]byte(result.Output))) != o.OutputSHA256 {
		return Pull{}, false
	}
	outcome := "tool_failed"
	if *result.OK {
		outcome = "tool_succeeded"
	}
	metadata := fmt.Sprintf("recovery observation; tool=%s; outcome=%s; check_sha256=%s", result.Name, outcome, o.CheckSHA256)
	// A read receipt can substantiate a captured source hash. A note alone
	// cannot assert that a file version was read or that it is current now.
	if o.SourceSHA256 != "" && result.Name == "read" && *result.OK {
		line, _, _ := strings.Cut(result.Output, "\n")
		var receipt struct {
			SHA256 string `json:"sha256"`
		}
		if strings.HasPrefix(line, "[read ") && strings.HasSuffix(line, "]") && json.Unmarshal([]byte(line[6:len(line)-1]), &receipt) == nil && digestValid(receipt.SHA256) && receipt.SHA256 == o.SourceSHA256 {
			metadata += "; source_sha256=" + receipt.SHA256
		}
	}
	return Pull{DocID: eventRef(sessionID, ev.ID), EvtID: ev.ID, SessionID: sessionID, Kind: "recovery_observation", SourceRef: eventRef(sessionID, source.ID), Content: "[" + metadata + "] " + evidenceExcerpt(result.Output, 2000), Live: true}, true
}

func errorEvidence(ev episodic.Event, sessionID string, byID map[string]episodic.Event) (Pull, bool) {
	if ev.Type == episodic.Note {
		return recoveryEvidence(ev, sessionID, byID)
	}
	if ev.Type != episodic.ToolResult && ev.Type != episodic.Err {
		return Pull{}, false
	}
	var p struct {
		Name, Output, Detail string
		SessionID            string `json:"session_id"`
		OK                   *bool
	}
	if json.Unmarshal(ev.Payload, &p) != nil || (p.SessionID != "" && p.SessionID != sessionID) || recursiveEvidence(p.Name) {
		return Pull{}, false
	}
	content := p.Detail
	if ev.Type == episodic.ToolResult {
		if p.Name == "" || p.Output == "" || p.OK == nil {
			return Pull{}, false
		}
		outcome := "tool_failed"
		if *p.OK {
			outcome = "tool_succeeded"
		}
		content = p.Name + ": [" + outcome + "] " + evidenceExcerpt(p.Output, 2000)
	}
	if strings.TrimSpace(content) == "" {
		return Pull{}, false
	}
	ref := eventRef(sessionID, ev.ID)
	return Pull{DocID: ref, EvtID: ev.ID, SessionID: sessionID, Kind: string(ev.Type), SourceRef: ref, Content: content, Live: true}, true
}

// Runtime diagnostics can sit between a long command prelude and loader stack.
// Preserve the diagnostic line and nearby frame when recognizable; otherwise
// retain both ends without interpreting text embedded in source as an error.
func evidenceExcerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if offset := diagnosticOffset(s); offset >= 0 {
		const before, after = "[earlier output omitted]\n", "\n[later output omitted]"
		prefix := ""
		if offset > 0 {
			prefix = before
		}
		available := n - len(prefix) - len(after)
		if available > 32 {
			end := min(len(s), offset+available)
			for end < len(s) && !utf8.RuneStart(s[end]) {
				end--
			}
			suffix := ""
			if end < len(s) {
				suffix = after
			}
			return prefix + s[offset:end] + suffix
		}
	}
	const marker = " …[omitted]… "
	head := (n - len(marker)) / 2
	tail := len(s) - head
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return s[:head] + marker + s[tail:]
}

func diagnosticOffset(s string) int {
	for start := 0; start < len(s); {
		end := strings.IndexByte(s[start:], '\n')
		if end < 0 {
			end = len(s)
		} else {
			end += start
		}
		line := strings.TrimSpace(s[start:end])
		uncaught := strings.HasPrefix(line, "Uncaught ")
		line = strings.TrimPrefix(strings.TrimPrefix(line, "Uncaught (in promise) "), "Uncaught ")
		header, message, hasColon := strings.Cut(line, ":")
		named := false
		for _, name := range []string{"TypeError", "ReferenceError", "RangeError", "SyntaxError", "Error", "AssertionError"} {
			if header == name || (strings.HasPrefix(header, name+" [") && strings.HasSuffix(header, "]")) {
				named = true
				break
			}
		}
		assertion := strings.EqualFold(header, "assertion failed")
		if hasColon && strings.TrimSpace(message) != "" && (named || assertion) {
			// Anchoring to a complete line excludes console.log/throw/new and
			// command arguments. Require runtime context for bare named errors
			// so source properties such as `TypeError: constructor` do not match.
			context := uncaught || assertion
			following := s[min(end+1, len(s)):min(len(s), end+1200)]
			for _, next := range strings.SplitN(following, "\n", 9)[:min(8, len(strings.SplitN(following, "\n", 9)))] {
				next = strings.TrimSpace(next)
				if strings.HasPrefix(next, "at ") && strings.Contains(next, ":") {
					context = true
					break
				}
			}
			previous := s[max(0, start-400):start]
			for _, prev := range strings.Split(previous, "\n") {
				prev = strings.TrimSpace(prev)
				if (strings.HasPrefix(prev, "File \"") && strings.Contains(prev, "\", line ")) || (strings.Contains(prev, "^") && strings.Trim(prev, "^~ \t") == "") {
					context = true
					break
				}
			}
			if context {
				return start
			}
		}
		start = end + 1
	}
	return -1
}

func pullContent(content string) string {
	// Preserve complete hashes in the typed observation header while spending
	// the remaining excerpt budget on both ends of the recorded result.
	if strings.HasPrefix(content, "[recovery observation;") {
		if end := strings.Index(content, "] "); end >= 0 && end+2 <= 320 {
			end += 2
			return content[:end] + evidenceExcerpt(content[end:], PullDocChars-end)
		}
	}
	// Tool outcome is historical provenance too. Keep its prefix when the
	// selected diagnostic occurs after a long result prelude.
	if end := strings.Index(content, ": [tool_"); end > 0 && end <= 80 {
		for _, outcome := range []string{": [tool_failed] ", ": [tool_succeeded] "} {
			if strings.HasPrefix(content[end:], outcome) {
				end += len(outcome)
				return content[:end] + evidenceExcerpt(content[end:], PullDocChars-end)
			}
		}
	}
	return evidenceExcerpt(content, PullDocChars)
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
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
