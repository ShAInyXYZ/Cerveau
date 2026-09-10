package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

// apply_patch validates against read output, which is now line-numbered.
// A raw source old_string must still validate and apply — otherwise every
// multi-file edit silently fails against the numbered content.
func TestApplyPatchAgainstNumberedRead(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "m.js"), []byte("function f() {\n  cpu.setDifficulty(x);\n  cpu.reset();\n}\n"), 0o644)

	reg := NewRegistry(
		Entry{Tool: NewRead(dir)},
		Entry{Tool: NewEdit(dir)},
		Entry{Tool: NewWrite(dir)},
	)
	reg.SetWorkspace(dir)
	ap := NewApplyPatch()
	ap.SetRegistry(reg)

	raw := json.RawMessage(`{"edits":[
		{"path":"m.js","old_string":"cpu.setDifficulty(x);","new_string":"ai.setDifficulty(x);"},
		{"path":"m.js","old_string":"cpu.reset();","new_string":"ai.reset();"}
	]}`)
	out, err := ap.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("apply_patch failed against numbered read: %v (%s)", err, out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "m.js"))
	if strings.Contains(string(got), "cpu.") {
		t.Fatalf("edits not applied: %q", string(got))
	}
}

// apply_patch must share edit's indentation tolerance: a hunk whose
// old_string differs only in leading whitespace should validate and apply,
// so a model doesn't succeed with edit but fail with apply_patch.
func TestApplyPatchForgivesIndentation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "m.js"), []byte("function f() {\n      deeplyIndented(x);\n}\n"), 0o644)
	reg := NewRegistry(Entry{Tool: NewRead(dir)}, Entry{Tool: NewEdit(dir)}, Entry{Tool: NewWrite(dir)})
	reg.SetWorkspace(dir)
	ap := NewApplyPatch()
	ap.SetRegistry(reg)

	// old_string has zero leading spaces; file has six.
	raw := json.RawMessage(`{"edits":[{"path":"m.js","old_string":"deeplyIndented(x);","new_string":"fixed(x);"}]}`)
	if out, err := ap.Execute(context.Background(), raw); err != nil {
		t.Fatalf("indentation-only mismatch should apply: %v (%s)", err, out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "m.js"))
	if !strings.Contains(string(got), "      fixed(x);") {
		t.Fatalf("indentation not preserved: %q", string(got))
	}
}

// One apply_patch instance is shared by every per-session registry. It must
// edit inside the registry that is EXECUTING it, not the one wired last: a
// Crane6 build patched files in the Crane folder (2026-09-04).
func TestApplyPatchUsesTheExecutingRegistry(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dirA, "game.html"), []byte("const WIN=80;\n"), 0o644)
	os.WriteFile(filepath.Join(dirB, "game.html"), []byte("const WIN=80;\n"), 0o644)
	ap := NewApplyPatch()
	regA := NewRegistry(Entry{Tool: NewRead(dirA)}, Entry{Tool: NewEdit(dirA)}, Entry{Tool: NewWrite(dirA)}, Entry{Tool: ap})
	regB := NewRegistry(Entry{Tool: NewRead(dirB)}, Entry{Tool: NewEdit(dirB)}, Entry{Tool: NewWrite(dirB)}, Entry{Tool: ap})
	ap.SetRegistry(regA) // the startup wiring points at A; B is the session running now
	args, _ := json.Marshal(map[string]any{"edits": []map[string]string{{"path": "game.html", "old_string": "WIN=80", "new_string": "WIN=8000"}}})
	if _, err := regB.ExecuteMode(context.Background(), "apply_patch", args, ""); err != nil {
		t.Fatalf("apply_patch via B: %v", err)
	}
	a, _ := os.ReadFile(filepath.Join(dirA, "game.html"))
	b, _ := os.ReadFile(filepath.Join(dirB, "game.html"))
	if !strings.Contains(string(b), "WIN=8000") || strings.Contains(string(a), "WIN=8000") {
		t.Fatalf("patch landed in the wrong workspace: A=%q B=%q", a, b)
	}
}

// The Minecraft run validated multiline hunks against read's numbered,
// cursor-dependent presentation. Valid source therefore failed to match.
func TestApplyPatchReadsCompleteRawSourceWithoutMovingCursor(t *testing.T) {
	for _, advance := range []bool{false, true} {
		t.Run(fmt.Sprintf("cursor_advanced_%t", advance), func(t *testing.T) {
			dir := t.TempDir()
			old := "function lightAt(x) {\n  return cached(x);\n}"
			content := strings.Repeat("// padding\n", 2000) + old + "\n"
			os.WriteFile(filepath.Join(dir, "world.js"), []byte(content), 0o644)
			reg := setupPatchReg(t, dir)
			read := reg.entries["read"].Tool.(*Read)
			if advance {
				if _, err := reg.ExecuteMode(context.Background(), "read", json.RawMessage(`{"path":"world.js"}`), ""); err != nil {
					t.Fatal(err)
				}
			}
			cursorBefore := read.next[filepath.Join(dir, "world.js")]
			args, _ := json.Marshal(map[string]any{"edits": []map[string]string{{
				"path": "world.js", "old_string": old, "new_string": "function lightAt(x) {\n  return queued(x);\n}",
			}}})
			if _, err := reg.ExecuteMode(context.Background(), "apply_patch", args, ""); err != nil {
				t.Fatalf("valid multiline hunk beyond read page rejected: %v", err)
			}
			got, _ := os.ReadFile(filepath.Join(dir, "world.js"))
			if string(got) != strings.Replace(content, "cached(x)", "queued(x)", 1) {
				t.Fatal("source content corrupted")
			}
			if read.next[filepath.Join(dir, "world.js")] != cursorBefore {
				t.Fatalf("patch changed public read cursor: before=%d after=%d", cursorBefore, read.next[filepath.Join(dir, "world.js")])
			}
		})
	}
}

func TestApplyPatchRawValidationStillUsesReadGuardAndJail(t *testing.T) {
	for _, scenario := range []string{"guard", "escape", "symlink", "oversize"} {
		t.Run(scenario, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			os.WriteFile(filepath.Join(dir, "a.txt"), []byte("first"), 0o644)
			os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("outside"), 0o644)
			path, old := "b.txt", "second"
			os.WriteFile(filepath.Join(dir, path), []byte(old), 0o644)
			reg := setupPatchReg(t, dir)
			switch scenario {
			case "guard":
				reg.SetGuard(func(tool string, args json.RawMessage) error {
					if tool == "read" && strings.Contains(string(args), "b.txt") {
						return fmt.Errorf("test read denied")
					}
					return nil
				})
			case "escape":
				path = "../outside.txt"
			case "symlink":
				os.Symlink(outside, filepath.Join(dir, "link"))
				path, old = "link/outside.txt", "outside"
			case "oversize":
				os.WriteFile(filepath.Join(dir, path), []byte(strings.Repeat("z", maxFileSize)+old), 0o644)
			}
			args, _ := json.Marshal(map[string]any{"edits": []map[string]string{
				{"path": "a.txt", "old_string": "first", "new_string": "changed"},
				{"path": path, "old_string": old, "new_string": "changed"},
			}})
			if _, err := reg.ExecuteMode(context.Background(), "apply_patch", args, ""); err == nil {
				t.Fatal("unsafe or unreadable hunk accepted")
			}
			got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
			if string(got) != "first" {
				t.Fatal("failed validation changed an earlier file")
			}
			got, _ = os.ReadFile(filepath.Join(outside, "outside.txt"))
			if string(got) != "outside" {
				t.Fatal("outside file changed")
			}
		})
	}
}

func TestApplyPatchMalformedHunksGiveSchemaWithoutWrites(t *testing.T) {
	for _, args := range []string{
		`{}`,
		`{"edits":[{"old_string":"original","new_string":"changed"}]}`,
		`{"path":"a.txt","edits":[{"old_string":"original","new_string":"changed"}]}`,
		`{"edits":[{"path":"a.txt","new_string":"changed"}]}`,
		`{"edits":[{"path":"a.txt","old_string":"original"}]}`,
		`{"edits":[{"path":"a.txt","old_string":"original","new_string":null}]}`,
	} {
		t.Run(args, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "a.txt"), []byte("original"), 0o644)
			reg := setupPatchReg(t, dir)
			_, err := reg.ExecuteMode(context.Background(), "apply_patch", json.RawMessage(args), "")
			if err == nil || !strings.Contains(err.Error(), `"edits":[{"path":`) || !strings.Contains(err.Error(), "no edits applied") {
				t.Fatalf("missing actionable schema/no-write error: %v", err)
			}
			got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
			if string(got) != "original" {
				t.Fatalf("malformed patch changed source: %q", got)
			}
		})
	}
}

func TestApplyPatchMismatchHintUsesActualLineAndRawBoundedText(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("// padding\n", 600) + "function lightAt(x) {\n  return cached(x);\n}\n"
	os.WriteFile(filepath.Join(dir, "world.js"), []byte(content), 0o644)
	reg := setupPatchReg(t, dir)
	args := json.RawMessage(`{"edits":[{"path":"world.js","old_string":"function lightAt(x) {\n  return stale(x);\n}","new_string":"changed"}]}`)
	_, err := reg.ExecuteMode(context.Background(), "apply_patch", args, "")
	if err == nil || !strings.Contains(err.Error(), "line 601") || !strings.Contains(err.Error(), "return cached(x);") || strings.Contains(err.Error(), `601\t`) {
		t.Fatalf("hint must show a current unnumbered region and absolute location: %v", err)
	}
	if len(err.Error()) > 2200 {
		t.Fatal("recovery hint is unbounded")
	}
}

func TestApplyPatchPrevalidatesSequentialHunksAndAliases(t *testing.T) {
	for _, alias := range []string{"a.txt", "./a.txt", "sub/../a.txt", "linked.txt", "hard.txt"} {
		for _, conflict := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_conflict_%t", alias, conflict), func(t *testing.T) {
				dir := t.TempDir()
				os.WriteFile(filepath.Join(dir, "a.txt"), []byte("original"), 0o644)
				os.WriteFile(filepath.Join(dir, "first.txt"), []byte("first"), 0o644)
				os.Symlink("a.txt", filepath.Join(dir, "linked.txt"))
				if err := os.Link(filepath.Join(dir, "a.txt"), filepath.Join(dir, "hard.txt")); err != nil {
					t.Fatal(err)
				}
				old := "changed"
				if conflict {
					old = "original" // no longer present after the previous hunk
				}
				args, _ := json.Marshal(map[string]any{"edits": []map[string]string{
					{"path": "first.txt", "old_string": "first", "new_string": "edited"},
					{"path": "a.txt", "old_string": "original", "new_string": "changed"},
					{"path": alias, "old_string": old, "new_string": "final"},
				}})
				reg := setupPatchReg(t, dir)
				_, err := reg.ExecuteMode(context.Background(), "apply_patch", args, "")
				if (err != nil) != conflict {
					t.Fatalf("conflict=%t: %v", conflict, err)
				}
				wantA, wantFirst := "final", "edited"
				if conflict {
					wantA, wantFirst = "original", "first"
					if !strings.Contains(err.Error(), "no edits applied") {
						t.Fatalf("must fail during validation: %v", err)
					}
				}
				for name, want := range map[string]string{"a.txt": wantA, "first.txt": wantFirst} {
					got, _ := os.ReadFile(filepath.Join(dir, name))
					if string(got) != want {
						t.Fatalf("%s = %q; want %q", name, got, want)
					}
				}
			})
		}
	}
}

func TestApplyPatchCreateThenEditIsPrevalidated(t *testing.T) {
	dir := t.TempDir()
	reg := setupPatchReg(t, dir)
	args := json.RawMessage(`{"edits":[{"path":"new.txt","old_string":"","new_string":"created"},{"path":"./new.txt","old_string":"created","new_string":"final"}]}`)
	if _, err := reg.ExecuteMode(context.Background(), "apply_patch", args, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "new.txt"))
	if string(got) != "final" {
		t.Fatalf("create+edit produced %q", got)
	}
}

func TestApplyPatchOversizedResultDoesNotPartiallyApply(t *testing.T) {
	for _, old := range []string{"", "original"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "first.txt"), []byte("first"), 0o644)
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("original"), 0o644)
		reg := setupPatchReg(t, dir)
		args, _ := json.Marshal(map[string]any{"edits": []map[string]string{
			{"path": "first.txt", "old_string": "first", "new_string": "edited"},
			{"path": "a.txt", "old_string": old, "new_string": strings.Repeat("x", maxFileSize+1)},
		}})
		_, err := reg.ExecuteMode(context.Background(), "apply_patch", args, "")
		if err == nil || !strings.Contains(err.Error(), "no edits applied") {
			t.Fatalf("oversized result must be rejected during validation: %v", err)
		}
		for file, want := range map[string]string{"first.txt": "first", "a.txt": "original"} {
			got, _ := os.ReadFile(filepath.Join(dir, file))
			if string(got) != want {
				t.Fatalf("%s changed after validation failure", file)
			}
		}
	}
}
