package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"cerveau/internal/episodic"
	"cerveau/internal/tools"
)

func sourceEvent(id string, kind episodic.EventType, payload any) episodic.Event {
	b, _ := json.Marshal(payload)
	return episodic.Event{ID: id, Type: kind, Payload: b}
}

func recoverySourceResult(t *testing.T, r *recoveryReader, args any) map[string]any {
	t.Helper()
	a, _ := json.Marshal(args)
	out, err := r.Execute(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRecoverySourceSearchDecodedNewestAndPairedOutcome(t *testing.T) {
	events := []episodic.Event{
		sourceEvent("evt_000001", episodic.ToolCall, map[string]any{"id": "old", "name": "write", "args": map[string]any{"path": "world.js", "content": "function flushUpdates() { return 'old'; }"}}),
		sourceEvent("evt_000002", episodic.ToolResult, map[string]any{"id": "old", "name": "write", "ok": true, "output": "wrote world.js"}),
		sourceEvent("evt_000003", episodic.ToolCall, map[string]any{"id": "new", "name": "write", "args": map[string]any{"path": "world.js", "content": "function flushUpdates() { return 'new'; }"}, "raw_args": "duplicate flushUpdates"}),
		sourceEvent("evt_000004", episodic.ToolResult, map[string]any{"id": "new", "name": "write", "ok": false, "output": "permission denied"}),
		sourceEvent("evt_000005", episodic.ToolCall, map[string]any{"id": "shell", "name": "bash", "args": map[string]any{"command": "grep flushUpdates world.js"}}),
		sourceEvent("evt_000006", episodic.ToolCall, map[string]any{"id": "other", "name": "write", "args": map[string]any{"path": "other.js", "content": "flushUpdates"}}),
	}
	_, r := recoveryHistory(events, t.TempDir())
	got := recoverySourceResult(t, r, map[string]any{"query": "flushUpdates", "path": "world.js"})
	hits := got["matches"].([]any)
	if len(hits) != 2 {
		t.Fatalf("noisy source search: %+v", got)
	}
	newest := hits[0].(map[string]any)
	if newest["ref"] != "evt_000003#args.content" || newest["execution"] != "tool_failed" || newest["result_event_id"] != "evt_000004" {
		t.Fatalf("missing outcome: %+v", newest)
	}
	if hits[1].(map[string]any)["execution"] != "tool_succeeded" {
		t.Fatalf("missing successful outcome: %+v", hits[1])
	}
	view := recoverySourceResult(t, r, map[string]any{"ref": newest["ref"], "offset": newest["offset"]})
	if view["source"] != "function flushUpdates() { return 'new'; }" || view["field"] != "args.content" {
		t.Fatalf("not decoded source: %+v", view)
	}
	if _, ok := view["matches_current"]; ok {
		t.Fatal("invented current-file comparison")
	}
	raw := recoverySourceResult(t, r, map[string]any{"id": "evt_000003"})
	if !strings.Contains(raw["source"].(string), "raw_args") {
		t.Fatal("explicit raw event access lost")
	}
}

func TestRecoverySourcePairsRepeatedCallIDsChronologically(t *testing.T) {
	events := []episodic.Event{}
	for i, ok := range []bool{true, false} {
		events = append(events,
			sourceEvent(fmt.Sprintf("evt_%06d", i*2+1), episodic.ToolCall, map[string]any{"id": "same", "name": "edit", "args": map[string]any{"path": "a.js", "old_string": "before", "new_string": fmt.Sprintf("after %d", i)}}),
			sourceEvent(fmt.Sprintf("evt_%06d", i*2+2), episodic.ToolResult, map[string]any{"id": "same", "name": "edit", "ok": ok, "output": "result"}),
		)
	}
	_, r := recoveryHistory(events, t.TempDir())
	got := recoverySourceResult(t, r, map[string]any{"query": "after"})
	hits := got["matches"].([]any)
	if len(hits) != 2 || hits[0].(map[string]any)["execution"] != "tool_failed" || hits[1].(map[string]any)["execution"] != "tool_succeeded" {
		t.Fatalf("wrong pairing: %+v", got)
	}
}

func TestRecoverySourceInterruptedCallCannotBorrowNewCallOutcome(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprintf("scoped=%t", scoped), func(t *testing.T) {
			oldRun, newRun := "", ""
			if scoped {
				oldRun, newRun = "old-run", "new-run"
			}
			_, r := recoveryHistory([]episodic.Event{
				sourceEvent("evt_000001", episodic.ToolCall, map[string]any{"run_id": oldRun, "id": "same", "name": "write", "args": map[string]any{"path": "a.js", "content": "interrupted version"}}),
				sourceEvent("evt_000002", episodic.ToolCall, map[string]any{"run_id": newRun, "id": "same", "name": "write", "args": map[string]any{"path": "a.js", "content": "successful version"}}),
				sourceEvent("evt_000003", episodic.ToolResult, map[string]any{"run_id": newRun, "id": "same", "name": "write", "ok": true, "output": "wrote"}),
			}, t.TempDir())
			got := recoverySourceResult(t, r, map[string]any{"query": "version"})["matches"].([]any)
			if len(got) != 2 || got[0].(map[string]any)["execution"] != "tool_succeeded" || got[1].(map[string]any)["execution"] != "unconfirmed" {
				t.Fatalf("orphan borrowed a later result: %+v", got)
			}
		})
	}
}

func TestRecoverySourceNewReadReceiptPreservesExactBytes(t *testing.T) {
	for _, source := range []string{"const value = 1;", "const value = 1;\n", "é", ""} {
		t.Run(fmt.Sprintf("%q", source), func(t *testing.T) {
			receipt, _ := json.Marshal(map[string]any{"path": "a.js", "sha256": strings.Repeat("a", 64), "start_offset": 100, "end_offset": 100 + len(source), "from_line": 10, "to_line": 10})
			output := "[read " + string(receipt) + "]\n10\t" + strings.TrimSuffix(source, "\n") + "\n"
			_, r := recoveryHistory([]episodic.Event{
				sourceEvent("evt_000001", episodic.ToolCall, map[string]any{"id": "read", "name": "read", "args": map[string]any{"path": "a.js"}}),
				sourceEvent("evt_000002", episodic.ToolResult, map[string]any{"id": "read", "name": "read", "ok": true, "output": output}),
			}, t.TempDir())
			got := recoverySourceResult(t, r, map[string]any{"ref": "evt_000002#output.source"})
			if got["source"] != source || got["captured_file_sha256"] != strings.Repeat("a", 64) {
				t.Fatalf("presentation corrupted source bytes: %+v", got)
			}
		})
	}
}

func TestRecoverySourceDecodesRealPaginatedReadWithoutInventedPartialLine(t *testing.T) {
	for _, source := range []string{strings.Repeat("const value = 'a complete line';\n", 500), strings.Repeat("é", 10000)} {
		t.Run(fmt.Sprintf("bytes=%d", len(source)), func(t *testing.T) {
			ws := t.TempDir()
			if err := os.WriteFile(filepath.Join(ws, "a.js"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			args := json.RawMessage(`{"path":"a.js","from_line":1,"to_line":500}`)
			output, err := tools.NewRead(ws).Execute(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			header, _, _ := strings.Cut(output, "\n")
			var receipt struct {
				Start      int  `json:"start_offset"`
				End        int  `json:"end_offset"`
				PartialEnd bool `json:"partial_end"`
			}
			if err = json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(header, "[read "), "]")), &receipt); err != nil {
				t.Fatal(err)
			}
			_, r := recoveryHistory([]episodic.Event{
				sourceEvent("evt_000001", episodic.ToolCall, map[string]any{"id": "read", "name": "read", "args": args}),
				sourceEvent("evt_000002", episodic.ToolResult, map[string]any{"id": "read", "name": "read", "ok": true, "output": output}),
			}, t.TempDir())
			got := recoverySourceResult(t, r, map[string]any{"ref": "evt_000002#output.source"})
			if got["source"] != source[receipt.Start:receipt.End] || got["partial_end"] != receipt.PartialEnd {
				t.Fatalf("captured bytes/partial flag diverged: %+v", got)
			}
		})
	}
}

func TestRecoverySourceMultilineMatchAlwaysIncludedInSnippet(t *testing.T) {
	_, r := recoveryHistory([]episodic.Event{sourceEvent("evt_000001", episodic.ToolCall, map[string]any{"name": "write", "args": map[string]any{"path": "a.js", "content": "first\nsecond" + strings.Repeat("x", 10000)}})}, t.TempDir())
	hit := recoverySourceResult(t, r, map[string]any{"query": "first\nsecond"})["matches"].([]any)[0].(map[string]any)
	if !strings.Contains(hit["snippet"].(string), "first\nsecond") {
		t.Fatalf("query cut off from match: %+v", hit)
	}
}

func TestRecoverySourcePatchFieldPaginationAndProvenance(t *testing.T) {
	var edits []map[string]any
	for i := 0; i < 26; i++ {
		edits = append(edits, map[string]any{"path": "a.js", "old_string": fmt.Sprintf("old%d", i), "new_string": fmt.Sprintf("matched source %d", i)})
	}
	_, r := recoveryHistory([]episodic.Event{
		sourceEvent("evt_000001", episodic.ToolCall, map[string]any{"id": "patch", "name": "apply_patch", "args": map[string]any{"edits": edits}}),
		sourceEvent("evt_000002", episodic.ToolResult, map[string]any{"id": "patch", "name": "apply_patch", "ok": false, "output": "partial failure"}),
	}, t.TempDir())
	seen := map[string]bool{}
	after := ""
	for {
		got := recoverySourceResult(t, r, map[string]any{"query": "matched source", "after": after})
		for _, item := range got["matches"].([]any) {
			hit := item.(map[string]any)
			ref := hit["ref"].(string)
			if seen[ref] || hit["execution"] != "tool_failed" {
				t.Fatalf("bad cursor or outcome: %+v", hit)
			}
			seen[ref] = true
		}
		if got["eof"] == true {
			break
		}
		after = got["next_after"].(string)
		if after == "" {
			t.Fatal("missing exact continuation")
		}
	}
	if len(seen) != 26 {
		t.Fatalf("fields lost across page: %d", len(seen))
	}
}

func TestRecoverySourceReadResultUsesActualLinesNotHeaderClaim(t *testing.T) {
	events := []episodic.Event{
		sourceEvent("evt_000001", episodic.ToolCall, map[string]any{"id": "r", "name": "read", "args": map[string]any{"path": "world.js", "from_line": 155, "to_line": 340}}),
		sourceEvent("evt_000002", episodic.ToolResult, map[string]any{"id": "r", "name": "read", "ok": true, "output": "[world.js lines 155-340 of 800]\n155\tconst world = {\n156\t  flushUpdates,\n157\t  rayca\n...[range too large — narrow from_line/to_line]"}),
	}
	_, r := recoveryHistory(events, t.TempDir())
	got := recoverySourceResult(t, r, map[string]any{"query": "const world"})
	hit := got["matches"].([]any)[0].(map[string]any)
	if hit["file"] != "world.js" || hit["from_line"] != float64(155) || hit["to_line"] != float64(157) || hit["partial_end"] != true {
		t.Fatalf("false coverage: %+v", hit)
	}
	view := recoverySourceResult(t, r, map[string]any{"ref": hit["ref"]})
	if view["source"] != "const world = {\n  flushUpdates,\n  rayca" {
		t.Fatalf("presentation leaked: %+v", view)
	}
}

func TestRecoverySourceLargeWriteMatchOffsetSkipsEnvelopeAndPrefix(t *testing.T) {
	content := strings.Repeat("// earlier source\n", 1800) + "const world = { flushUpdates };\nreturn world;\n"
	_, r := recoveryHistory([]episodic.Event{sourceEvent("evt_000001", episodic.ToolCall, map[string]any{"id": "write", "name": "write", "args": map[string]any{"path": "world.js", "content": content}, "raw_args": content})}, t.TempDir())
	hit := recoverySourceResult(t, r, map[string]any{"query": "const world"})["matches"].([]any)[0].(map[string]any)
	if hit["offset"].(float64) < 30000 || !strings.Contains(hit["snippet"].(string), "const world") {
		t.Fatalf("match not directly reachable: %+v", hit)
	}
	view := recoverySourceResult(t, r, map[string]any{"ref": hit["ref"], "offset": hit["offset"]})
	if !strings.Contains(view["source"].(string), "return world;") || view["total_bytes"] != float64(len(content)) {
		t.Fatalf("envelope charged to source: %+v", view)
	}
}

func TestRecoverySourceUTF8ContinuationAndCurrentEquality(t *testing.T) {
	ws := t.TempDir()
	content := strings.Repeat("é", 10000) + "\nend\n"
	if err := os.WriteFile(filepath.Join(ws, "world.js"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	_, r := recoveryHistory([]episodic.Event{sourceEvent("evt_000001", episodic.ToolCall, map[string]any{"id": "write", "name": "write", "args": map[string]any{"path": "world.js", "content": content}})}, t.TempDir(), ws)
	var rebuilt strings.Builder
	offset := float64(0)
	for {
		got := recoverySourceResult(t, r, map[string]any{"ref": "evt_000001#args.content", "offset": offset})
		part := got["source"].(string)
		if !utf8.ValidString(part) || len(part) > 12000 || got["matches_current"] != true || got["execution"] != "unconfirmed" {
			t.Fatalf("false/unsafe source receipt: %+v", got)
		}
		rebuilt.WriteString(part)
		if got["eof"] == true {
			break
		}
		if got["next_offset"].(float64) <= offset {
			t.Fatal("cursor made no progress")
		}
		offset = got["next_offset"].(float64)
	}
	if rebuilt.String() != content {
		t.Fatal("UTF-8 continuation lost or duplicated source")
	}
	for _, args := range []string{`{"ref":"evt_000001#args.content","offset":1}`, `{"ref":"evt_000001#raw_args"}`, `{"query":"world","path":"../outside.js"}`, `{"ref":"evt_000001#args.content","id":"evt_000001"}`} {
		if _, err := r.Execute(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
}
