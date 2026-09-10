package loop

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"cerveau/internal/llm"
	"cerveau/internal/window"
)

func TestCompressWithoutWindowKeepsOnlyLatestRecoveryContext(t *testing.T) {
	items := []window.Item{
		{Kind: "system", Msg: llm.Message{Role: "system", Content: "system"}},
		{Kind: "dropped", Msg: llm.Message{Role: "user", Content: "baseline check"}},
		{Kind: "dropped", Msg: llm.Message{Role: "user", Content: "baseline source focus"}},
		{Kind: "assistant", Msg: llm.Message{Role: "assistant", Content: "first repair"}},
		{Kind: "dropped", Msg: llm.Message{Role: "user", Content: "first repair check"}},
		{Kind: "dropped", Msg: llm.Message{Role: "user", Content: "first repair source focus"}},
		{Kind: "assistant", Msg: llm.Message{Role: "assistant", Content: "second repair"}},
		{Kind: "pinned", Msg: llm.Message{Role: "user", Content: "latest committed check"}},
		{Kind: "pinned", Msg: llm.Message{Role: "user", Content: "latest source focus"}},
	}
	msgs, _ := (&Loop{}).compress(context.Background(), items)
	want := []llm.Message{items[0].Msg, items[3].Msg, items[6].Msg, items[7].Msg, items[8].Msg}
	if !reflect.DeepEqual(msgs, want) {
		t.Fatalf("obsolete focus/check survived replacement: got=%+v want=%+v", msgs, want)
	}
}

func TestCompressWithoutWindowStillRepairsToolGroups(t *testing.T) {
	items := []window.Item{
		{Kind: "dropped", Msg: llm.Message{Role: "user", Content: "obsolete source focus"}},
		{Kind: "tool", Msg: llm.Message{Role: "tool", ToolCallID: "orphan", Content: "orphan result"}},
		{Kind: "assistant", Msg: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "complete", Type: "function", Function: llm.FunctionCall{Name: "read", Arguments: `{"path":"world.js"}`}},
			{ID: "interrupted", Type: "function", Function: llm.FunctionCall{Name: "read", Arguments: `{"path":`}},
		}}},
		{Kind: "tool", Msg: llm.Message{Role: "tool", ToolCallID: "complete", Content: "recorded source"}},
		{Kind: "pinned", Msg: llm.Message{Role: "user", Content: "latest source focus"}},
	}
	msgs, _ := (&Loop{}).compress(context.Background(), items)
	if len(msgs) != 4 || msgs[0].Role != "assistant" || len(msgs[0].ToolCalls) != 2 || msgs[0].ToolCalls[1].Function.Arguments != "{}" {
		t.Fatalf("tool calls were not repaired: %+v", msgs)
	}
	if !reflect.DeepEqual(msgs[1], items[3].Msg) {
		t.Fatalf("recorded result changed: %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "interrupted" || !strings.Contains(msgs[2].Content, "Outcome not recorded") {
		t.Fatalf("missing result was not made explicit: %+v", msgs[2])
	}
	if !reflect.DeepEqual(msgs[3], items[4].Msg) || items[2].Msg.ToolCalls[1].Function.Arguments != `{"path":` {
		t.Fatal("latest focus or original tool call was modified")
	}
}
