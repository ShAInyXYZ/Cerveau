package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupPatchReg(t *testing.T, ws string) *Registry {
	t.Helper()
	reg := NewRegistry(
		Entry{Tool: NewRead(ws), RiskTier: RiskSafe},
		Entry{Tool: NewEdit(ws), RiskTier: RiskSensitive},
		Entry{Tool: NewWrite(ws), RiskTier: RiskSensitive},
	)
	reg.SetGuard(func(tool string, args json.RawMessage) error { return nil })
	ap := NewApplyPatch()
	ap.SetRegistry(reg)
	reg.entries["apply_patch"] = Entry{Tool: ap, RiskTier: RiskSensitive}
	return reg
}

func TestApplyPatchMultiFileAtomic(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "a.txt"), []byte("alpha one"), 0o644)
	os.WriteFile(filepath.Join(ws, "b.txt"), []byte("beta two"), 0o644)
	reg := setupPatchReg(t, ws)

	// One bad hunk (old_string missing) → NOTHING applied.
	args, _ := json.Marshal(map[string]any{"edits": []map[string]string{
		{"path": "a.txt", "old_string": "one", "new_string": "1"},
		{"path": "b.txt", "old_string": "NOT-PRESENT", "new_string": "2"},
	}})
	if _, err := reg.ExecuteMode(context.Background(), "apply_patch", args, ""); err == nil {
		t.Fatal("bad hunk not rejected")
	}
	a, _ := os.ReadFile(filepath.Join(ws, "a.txt"))
	if string(a) != "alpha one" {
		t.Fatalf("atomicity violated — a.txt was edited: %q", a)
	}

	// All valid → all applied, in one call.
	args, _ = json.Marshal(map[string]any{"edits": []map[string]string{
		{"path": "a.txt", "old_string": "one", "new_string": "1"},
		{"path": "b.txt", "old_string": "two", "new_string": "2"},
		{"path": "c.txt", "old_string": "", "new_string": "created"},
	}})
	out, err := reg.ExecuteMode(context.Background(), "apply_patch", args, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3/3") {
		t.Fatalf("report wrong: %q", out)
	}
	for f, want := range map[string]string{"a.txt": "alpha 1", "b.txt": "beta 2", "c.txt": "created"} {
		got, _ := os.ReadFile(filepath.Join(ws, f))
		if string(got) != want {
			t.Errorf("%s = %q, want %q", f, got, want)
		}
	}
}

func TestApplyPatchAmbiguousAndLimit(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "dup.txt"), []byte("x x"), 0o644)
	reg := setupPatchReg(t, ws)

	args, _ := json.Marshal(map[string]any{"edits": []map[string]string{
		{"path": "dup.txt", "old_string": "x", "new_string": "y"},
	}})
	if _, err := reg.ExecuteMode(context.Background(), "apply_patch", args, ""); err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("ambiguous match not caught: %v", err)
	}

	var hunks []map[string]string
	for i := 0; i < patchMaxHunks+1; i++ {
		hunks = append(hunks, map[string]string{"path": "dup.txt", "old_string": "", "new_string": "y"})
	}
	args, _ = json.Marshal(map[string]any{"edits": hunks})
	if _, err := reg.ExecuteMode(context.Background(), "apply_patch", args, ""); err == nil || !strings.Contains(err.Error(), "max") {
		t.Fatalf("hunk limit not enforced: %v", err)
	}
}
