package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/memory"
	"cerveau/internal/tools"
)

var turnCloseSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary":   map[string]any{"type": "string"},
		"decisions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"promotion_candidates": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content":  map[string]any{"type": "string"},
					"category": map[string]any{"type": "string", "enum": []string{"fact", "decision", "preference"}},
				},
				"required": []string{"content", "category"},
			},
		},
		"open_loops": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"required": []string{"summary", "decisions", "promotion_candidates", "open_loops"},
}

type turnMeta struct {
	Summary   string   `json:"summary"`
	Decisions []string `json:"decisions"`
	Promotion []struct {
		Content  string `json:"content"`
		Category string `json:"category"`
	} `json:"promotion_candidates"`
	OpenLoops []string `json:"open_loops"`
}

func (l *Loop) SetCurator(c *memory.Curator) { l.curator = c }

// SetInstantCheck wires a predicate telling the loop whether a session is an
// ephemeral instant session — those never promote to long-term semantic memory.
func (l *Loop) SetInstantCheck(f func(id string) bool) { l.isInstant = f }

func (l *Loop) boundaryHooks(sessionID string) {
	if l.curator == nil {
		return
	}
	// instant scratch sessions leave no long-term trace — no distill, no promotion
	if l.isInstant != nil && l.isInstant(sessionID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	meta, err := l.distill(ctx, sessionID)
	if err != nil {
		if wr, werr := l.open(sessionID); werr == nil {
			wr.Append(episodic.Err, map[string]string{"class": "boundary", "detail": "turn_close distill failed: " + err.Error()})
		}
		return
	}
	promoted := 0
	for _, cand := range meta.Promotion {
		if _, err := l.curator.Write(ctx, memory.Candidate{
			Content:      cand.Content,
			Category:     cand.Category,
			SessionID:    sessionID,
			SourceEvtIDs: []string{sessionID},
		}); err == nil {
			promoted++
		}
	}
	if wr, werr := l.open(sessionID); werr == nil {
		wr.Append(episodic.Note, map[string]any{
			"kind":       "turn_summary",
			"summary":    meta.Summary,
			"decisions":  strings.Join(meta.Decisions, " | "),
			"open_loops": strings.Join(meta.OpenLoops, " | "),
			"promoted":   fmt.Sprint(promoted),
		})
	}
}

func (l *Loop) distill(ctx context.Context, sessionID string) (*turnMeta, error) {
	events, err := episodic.Replay(l.path(sessionID))
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	count := 0
	for i := len(events) - 1; i >= 0 && count < 6; i-- {
		ev := events[i]
		if ev.Type != episodic.MsgUser && ev.Type != episodic.MsgAssistant && ev.Type != episodic.ToolResult {
			continue
		}
		content := ""
		var p struct {
			Text   string `json:"text"`
			Output string `json:"output"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil {
			content = p.Text + p.Output
		}
		if content == "" {
			continue
		}
		if len(content) > 300 {
			content = content[:300]
		}
		sb.WriteString(fmt.Sprintf("[%s] %s\n", ev.Type, content))
		count++
	}
	grammar, err := tools.SchemaToGBNF(turnCloseSchema)
	if err != nil {
		return nil, fmt.Errorf("turn_close grammar: %w", err)
	}
	messages := []llm.Message{
		{Role: "system", Content: "You distill a finished turn into structured metadata. Output only the JSON object. summary: one line. decisions: choices made this turn (may be empty). promotion_candidates: durable facts/preferences/decisions worth long-term memory (may be empty, be conservative). open_loops: unresolved threads (may be empty)."},
		{Role: "user", Content: "Turn transcript (newest last, truncated):\n" + sb.String()},
	}
	reply, _, err := l.llm.Complete(ctx, messages, nil, grammar, 1024)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(reply.Content)
	if content == "" {
		// nothing distilled — a valid outcome (short/empty turn), not a failure
		return &turnMeta{}, nil
	}
	var meta turnMeta
	if err := json.Unmarshal([]byte(content), &meta); err != nil {
		// grammar-constrained output can still get truncated at the token cap;
		// a broken distill must NOT surface as a turn error — the answer already
		// shipped. Degrade to an empty meta (nothing promoted this turn).
		return &turnMeta{}, nil
	}
	return &meta, nil
}
