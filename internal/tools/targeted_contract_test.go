package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func testSourceSHA(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

func toolMetadata(t *testing.T, kind, out string) map[string]any {
	t.Helper()
	line := strings.SplitN(out, "\n", 2)[0]
	prefix := "[" + kind + " "
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "]") {
		t.Fatalf("missing %s metadata: %.250s", kind, out)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, prefix), "]")), &meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

func TestReadRangeReportsActualCompleteCoverageAndContinuation(t *testing.T) {
	dir := t.TempDir()
	var source strings.Builder
	for i := 1; i <= 400; i++ {
		fmt.Fprintf(&source, "line %03d %s\n", i, strings.Repeat("x", 60))
	}
	body := source.String()
	if err := os.WriteFile(filepath.Join(dir, "world.js"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRead(dir)
	args := map[string]any{"path": "world.js", "from_line": 1, "to_line": 340}
	wantStart, wantLine, pages := 0, 1, 0
	for {
		raw, _ := json.Marshal(args)
		out, err := r.Execute(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		meta := toolMetadata(t, "read", out)
		if len(out) > readCapChars || CapIngress(out, readCapChars) != out {
			t.Fatalf("unbounded read: %d", len(out))
		}
		if meta["sha256"] != testSourceSHA(body) || int(meta["start_offset"].(float64)) != wantStart || int(meta["from_line"].(float64)) != wantLine {
			t.Fatalf("inaccurate source provenance or continuation: %v", meta)
		}
		last := int(meta["to_line"].(float64))
		end := int(meta["end_offset"].(float64))
		if meta["partial_start"] != false || meta["partial_end"] != false || body[end-1] != '\n' {
			t.Fatalf("ordinary source lines fragmented: %v", meta)
		}
		if !strings.Contains(out, fmt.Sprintf("%d\tline %03d %s\n", last, last, strings.Repeat("x", 60))) {
			t.Fatalf("advertised final line not fully returned: %v", meta)
		}
		pages++
		if meta["next_offset"] == nil {
			if last != 340 || meta["eof"] != false {
				t.Fatalf("requested end not honoured: %v", meta)
			}
			break
		}
		wantStart, wantLine = int(meta["next_offset"].(float64)), last+1
		if int(meta["next_line"].(float64)) != wantLine {
			t.Fatalf("wrong next line: %v", meta)
		}
		args["offset"], args["expected_sha256"] = wantStart, meta["sha256"]
		if pages > 10 {
			t.Fatal("read continuation did not converge")
		}
	}
	if pages < 2 {
		t.Fatal("fixture did not exercise pagination")
	}
}

func TestReadOversizedUnicodeLineFragmentsHaveExactByteCursors(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("雪🌍", 4000) + "\nlast\n"
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRead(dir)
	args := map[string]any{"path": "large.txt", "offset": 0}
	next, sawPartial := 0, false
	for calls := 0; calls < 20; calls++ {
		raw, _ := json.Marshal(args)
		out, err := r.Execute(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		meta := toolMetadata(t, "read", out)
		start, end := int(meta["start_offset"].(float64)), int(meta["end_offset"].(float64))
		if !utf8.ValidString(out) || start != next || end <= start || !utf8.ValidString(body[start:end]) {
			t.Fatalf("invalid UTF-8 or skipped source: %v", meta)
		}
		if meta["partial_end"] == true {
			sawPartial = true
		}
		if meta["next_offset"] == nil {
			if end != len(body) || meta["eof"] != true || !sawPartial {
				t.Fatalf("incomplete pagination: %v", meta)
			}
			return
		}
		next = int(meta["next_offset"].(float64))
		args["offset"] = next
	}
	t.Fatal("oversized line did not finish")
}

func TestReadVersionRejectsStaleContinuationAndResetsAutomaticCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	before := strings.Repeat("before\n", 4000)
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRead(dir)
	first, _ := r.Execute(context.Background(), json.RawMessage(`{"path":"big.txt"}`))
	meta := toolMetadata(t, "read", first)
	after := "changed\n" + before
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"path": "big.txt", "offset": meta["next_offset"], "expected_sha256": meta["sha256"]})
	if _, err := r.Execute(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale continuation accepted: %v", err)
	}
	out, err := r.Execute(context.Background(), json.RawMessage(`{"path":"big.txt"}`))
	if err != nil || !strings.Contains(out, "1\tchanged\n") {
		t.Fatalf("automatic cursor skipped changed beginning: %v %.300s", err, out)
	}
}

func TestVersionGuardedEditAndBoundedReceipt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world.js")
	before := "first\nconst answer = 1;\nlast\n"
	if err := os.WriteFile(path, []byte(before), 0o750); err != nil {
		t.Fatal(err)
	}
	e := NewEdit(dir)
	a := map[string]any{"path": "world.js", "old_string": "const answer = 1;", "new_string": "const answer = 2;", "expected_sha256": testSourceSHA("stale")}
	raw, _ := json.Marshal(a)
	if _, err := e.Execute(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale edit accepted: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != before {
		t.Fatal("stale edit changed source")
	}
	a["expected_sha256"] = testSourceSHA(before)
	raw, _ = json.Marshal(a)
	out, err := e.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	after := strings.Replace(before, "= 1", "= 2", 1)
	meta := toolMetadata(t, "edit", out)
	if meta["before_sha256"] != testSourceSHA(before) || meta["sha256"] != testSourceSHA(after) || meta["from_line"] != float64(2) || meta["to_line"] != float64(2) {
		t.Fatalf("bad mutation receipt: %v", meta)
	}
	if len(out) > 2000 {
		t.Fatalf("unbounded receipt: %d", len(out))
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o750 {
		t.Fatal("edit changed existing file mode")
	}
}

func TestWriteRequiresExplicitContentAndHonoursVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := NewWrite(dir)
	for _, args := range []string{`{"path":"keep.txt"}`, `{"path":"keep.txt","content":null}`, `{"path":"keep.txt","content":"bad","expected_sha256":"bad"}`} {
		if _, err := w.Execute(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatalf("unsafe write accepted: %s", args)
		}
		got, _ := os.ReadFile(path)
		if string(got) != "keep" {
			t.Fatal("rejected write truncated source")
		}
	}
	raw, _ := json.Marshal(map[string]string{"path": "keep.txt", "content": "", "expected_sha256": testSourceSHA("keep")})
	if _, err := w.Execute(context.Background(), raw); err != nil {
		t.Fatalf("explicit guarded empty content rejected: %v", err)
	}
}

func TestPatchVersionGuardUsesSequentialProjectedState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha one"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := setupPatchReg(t, dir)
	hunks := []map[string]string{
		{"path": "a.txt", "old_string": "one", "new_string": "two", "expected_sha256": testSourceSHA("alpha one")},
		{"path": "a.txt", "old_string": "two", "new_string": "three", "expected_sha256": testSourceSHA("alpha one")},
	}
	raw, _ := json.Marshal(map[string]any{"edits": hunks})
	if _, err := reg.ExecuteMode(context.Background(), "apply_patch", raw, ""); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale projected hunk accepted: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "alpha one" {
		t.Fatal("prevalidation applied partial patch")
	}
	hunks[1]["expected_sha256"] = testSourceSHA("alpha two")
	raw, _ = json.Marshal(map[string]any{"edits": hunks})
	out, err := reg.ExecuteMode(context.Background(), "apply_patch", raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, testSourceSHA("alpha three")) {
		t.Fatalf("patch discarded edit receipts: %s", out)
	}
}

func TestPatchOverwriteVersionGuardCannotBypassPrevalidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := setupPatchReg(t, dir)
	hunk := map[string]string{"path": "a.txt", "old_string": "", "new_string": "replacement", "expected_sha256": testSourceSHA("stale")}
	raw, _ := json.Marshal(map[string]any{"edits": []map[string]string{hunk}})
	if _, err := reg.ExecuteMode(context.Background(), "apply_patch", raw, ""); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale overwrite allowed: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "original" {
		t.Fatal("stale overwrite mutated source")
	}
	hunk["expected_sha256"] = testSourceSHA("original")
	raw, _ = json.Marshal(map[string]any{"edits": []map[string]string{hunk}})
	if _, err := reg.ExecuteMode(context.Background(), "apply_patch", raw, ""); err != nil {
		t.Fatal(err)
	}
}

func TestPatchExecutionRejectsSourceChangedByGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := setupPatchReg(t, dir)
	reg.SetGuard(func(name string, args json.RawMessage) error {
		if name == "edit" {
			return os.WriteFile(path, []byte("other old"), 0o644)
		}
		return nil
	})
	_, err := reg.ExecuteMode(context.Background(), "apply_patch", json.RawMessage(`{"edits":[{"path":"a.txt","old_string":"old","new_string":"new"}]}`), "")
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "0/1") {
		t.Fatalf("changed prevalidated source accepted: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "other old" {
		t.Fatal("patch overwrote guard's concurrent source change")
	}
}

func TestMutationReceiptLargeUnicodeReplacementBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	before, after := strings.Repeat("雪🌍", 1000), strings.Repeat("世界\n", 1000)
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"path": "big.txt", "old_string": before, "new_string": after})
	out, err := NewEdit(dir).Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	meta := toolMetadata(t, "edit", out)
	if len(out) > 2000 || !utf8.ValidString(out) || meta["removed_bytes"] != float64(len(before)) || meta["added_bytes"] != float64(len(after)) || !strings.Contains(out, "excerpt truncated") {
		t.Fatalf("bad bounded receipt: %.500s", out)
	}
}

func TestReadReceiptSurvivesReplayIngressCap(t *testing.T) {
	for _, fixture := range []struct{ name, path, content string }{
		{"normal", "world.js", strings.Repeat("const longEnoughSourceLine = 'abcdefghijklmnopqrstuvwxyz0123456789';\n", 500)},
		{"unicode_fragment", "unicode.js", strings.Repeat("雪🌍", 4000) + "\nlast\n"},
		{"long_path", strings.Repeat(strings.Repeat("p", 180)+"/", 12) + "world.js", strings.Repeat("雪🌍 code\n", 3000)},
		{"escaped_path", strings.Repeat(strings.Repeat("<&\"\t", 30)+"/", 10) + "world.js", strings.Repeat("source();\n", 2000)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ws := t.TempDir()
			full := filepath.Join(ws, fixture.path)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(fixture.content), 0o644); err != nil {
				t.Fatal(err)
			}
			r := NewRead(ws)
			next, pages := 0, 0
			for {
				args := map[string]any{"path": fixture.path, "offset": next}
				if fixture.name == "normal" {
					args["from_line"], args["to_line"] = 1, strings.Count(fixture.content, "\n")+1
				} else if pages%2 != 0 {
					// Automatic continuation adds another notice: include it in
					// the same whole-result/replay budget assertion.
					delete(args, "offset")
				}
				raw, _ := json.Marshal(args)
				out, err := r.Execute(context.Background(), raw)
				if err != nil {
					t.Fatal(err)
				}
				if len(out) > 8000 || CapIngress(out, 8000) != out {
					t.Fatalf("replay truncates source beneath receipt: %d bytes", len(out))
				}
				meta := toolMetadata(t, "read", out)
				start, end := int(meta["start_offset"].(float64)), int(meta["end_offset"].(float64))
				var source strings.Builder
				for _, line := range strings.Split(strings.SplitN(out, "\n", 2)[1], "\n") {
					_, text, numbered := strings.Cut(line, "\t")
					if !numbered {
						break
					}
					source.WriteString(text)
					source.WriteByte('\n')
				}
				captured := source.String()
				if start != next || end <= start || len(captured) < end-start || captured[:end-start] != fixture.content[start:end] || !utf8.ValidString(out) {
					t.Fatalf("inexact decoded coverage after replay: %v", meta)
				}
				pages++
				if meta["eof"] == true {
					if end != len(fixture.content) {
						t.Fatal("false EOF")
					}
					break
				}
				next = int(meta["next_offset"].(float64))
				if pages > 30 {
					t.Fatal("pagination stalled")
				}
			}
			if pages < 2 {
				t.Fatal("fixture missed truncation risk")
			}
		})
	}
}
