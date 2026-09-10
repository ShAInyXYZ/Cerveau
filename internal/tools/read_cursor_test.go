package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshReadCursorScopesDoNotSharePagination(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("A", readCapChars)+strings.Repeat("B", readCapChars)+"TAIL"), 0o644)
	r := NewRead(dir)
	call := json.RawMessage(`{"path":"big.txt"}`)
	a := WithFreshReadCursor(context.Background())
	b := WithFreshReadCursor(a) // a new step must not inherit the old window's cursor
	for _, ctx := range []context.Context{a, b, context.Background()} {
		first, err := r.Execute(ctx, call)
		if err != nil || !strings.Contains(first, "1\tAAA") {
			t.Fatalf("fresh scope inherited another scope's cursor: %v %q", err, first[:min(80, len(first))])
		}
	}
	for _, ctx := range []context.Context{a, b, context.Background()} {
		second, err := r.Execute(ctx, call)
		if err != nil || !strings.Contains(second, "continuing") || !strings.Contains(second, "BBB") {
			t.Fatalf("repeat within a scope did not continue: %v %q", err, second[:min(80, len(second))])
		}
	}
	// Existing explicit-offset behavior remains available inside a scope.
	out, err := r.Execute(a, json.RawMessage(`{"path":"big.txt","offset":0}`))
	if err != nil || !strings.Contains(out, "1\tAAA") {
		t.Fatalf("explicit offset ignored in scoped read: %v", err)
	}
}
