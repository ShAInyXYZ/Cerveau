package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditAndPatchRejectAmbiguousIndentationWithoutWrites(t *testing.T) {
	for _, tool := range []string{"edit", "apply_patch"} {
		t.Run(tool, func(t *testing.T) {
			dir := t.TempDir()
			body := "  same();\n    next();\n      same();\n        next();\n"
			os.WriteFile(filepath.Join(dir, "a.txt"), []byte(body), 0o644)
			edit := map[string]string{"path": "a.txt", "old_string": "same();\nnext();", "new_string": "changed();\n"}
			var arg any = edit
			if tool == "apply_patch" {
				arg = map[string]any{"edits": []map[string]string{edit}}
			}
			raw, _ := json.Marshal(arg)
			reg := setupPatchReg(t, dir)
			if _, err := reg.ExecuteMode(context.Background(), tool, raw, ""); err == nil {
				t.Fatal("ambiguous indentation-only edit accepted")
			}
			got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
			if string(got) != body {
				t.Fatalf("ambiguous edit changed source: %q", got)
			}
		})
	}
}

func TestEditMissingReplacementDoesNotDelete(t *testing.T) {
	for _, args := range []string{
		`{"path":"a.txt","old_string":"original"}`,
		`{"path":"a.txt","old_string":"original","new_string":null}`,
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("original"), 0o644)
		if _, err := NewEdit(dir).Execute(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatal("missing/null replacement accepted")
		}
		got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
		if string(got) != "original" {
			t.Fatal("malformed edit deleted source")
		}
	}
}

func TestNearestHintIsBoundedEvenForEscapedSource(t *testing.T) {
	hint := nearestHint("function lightAt(x) {"+strings.Repeat("\x01", 10000), "function lightAt(wrong)")
	if len(hint) > 1900 {
		t.Fatalf("hint is too large: %d bytes", len(hint))
	}
}
