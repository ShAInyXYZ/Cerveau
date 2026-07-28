package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type QuestionBroker func(ctx context.Context, sessionID, question string, options []string) (string, error)

type AskUser struct {
	broker QuestionBroker
	sctx   *SessionContext
}

func NewAskUser(b QuestionBroker, sctx *SessionContext) *AskUser {
	return &AskUser{broker: b, sctx: sctx}
}

func (t *AskUser) Name() string { return "ask_user" }

func (t *AskUser) Description() string {
	return "Park the loop and ask the user a question. Provide one-tap options when possible. Use for decisions you cannot make alone."
}

func (t *AskUser) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{"type": "string"},
			"options": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "one-tap choices; user can always answer freely or say 'decide yourself'",
			},
		},
		"required": []string{"question"},
	}
}

func (t *AskUser) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Question) == "" {
		return "", fmt.Errorf("question required")
	}
	if t.broker == nil || t.sctx == nil || t.sctx.SessionID == "" {
		return "", fmt.Errorf("no question broker available — decide yourself and note the assumption")
	}
	answer, err := t.broker(ctx, t.sctx.SessionID, a.Question, a.Options)
	if err != nil {
		return "", err
	}
	return answer, nil
}
