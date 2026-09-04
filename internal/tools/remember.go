package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"cerveau/internal/memory"
)

type SessionContext struct {
	SessionID string
	LastEvtID string
}

type Remember struct {
	curator *memory.Curator
	sctx    *SessionContext
}

func NewRemember(c *memory.Curator, sctx *SessionContext) *Remember {
	return &Remember{curator: c, sctx: sctx}
}

func (t *Remember) Name() string { return "remember" }

func (t *Remember) Description() string {
	return "Save a fact, decision, or preference to long-term semantic memory. Use for anything worth keeping across sessions."
}

func (t *Remember) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{"type": "string", "description": "the distilled fact to remember"},
			"category": map[string]any{
				"type": "string",
				"enum": []string{"fact", "decision", "preference"},
			},
		},
		"required": []string{"content", "category"},
	}
}

func (t *Remember) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Content  string `json:"content"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Content == "" {
		return "", fmt.Errorf("content required")
	}
	// The session is the RUN's, from its context. LastEvtID lives only on the
	// shared context and belongs to whichever session last started a chat —
	// use it only when that is this session, or the reference points into
	// another session's log.
	sid := SessionOf(ctx, t.sctx)
	src := []string{}
	if sid != "" {
		ref := sid
		if t.sctx != nil && t.sctx.SessionID == sid && t.sctx.LastEvtID != "" {
			ref += ":" + t.sctx.LastEvtID
		}
		src = append(src, ref)
	}
	res, err := t.curator.Write(ctx, memory.Candidate{
		Content:      a.Content,
		Category:     a.Category,
		SessionID:    sid,
		SourceEvtIDs: src,
	})
	if err != nil {
		return "", err
	}
	switch res.Action {
	case "merged":
		return fmt.Sprintf("already known — reinforced (%s)", res.DocID), nil
	case "linked":
		return fmt.Sprintf("saved, but similar to an existing memory — flagged for review (%s)", res.DocID), nil
	case "queued":
		return "memory store offline — queued for later", nil
	default:
		return fmt.Sprintf("remembered (%s)", res.DocID), nil
	}
}
