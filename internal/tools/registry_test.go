package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaToGBNFObject(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
			"flag": map[string]any{"type": "boolean"},
		},
		"required": []string{"path"},
	}
	g, err := SchemaToGBNF(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ws ::= [ \\t\\n]*",
		`"\"" "path" "\"" ws ":" ws`,
		`"\"" "flag" "\"" ws ":" ws`,
		"( \",\" ws",
		`"true" | "false"`,
	} {
		if !strings.Contains(g, want) {
			t.Fatalf("grammar missing %q:\n%s", want, g)
		}
	}
}

func TestSchemaToGBNFEnum(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string", "enum": []any{"done", "wip", "failed"}},
		},
		"required": []string{"status"},
	}
	g, err := SchemaToGBNF(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, lit := range []string{`"done"`, `"wip"`, `"failed"`} {
		if !strings.Contains(g, lit) {
			t.Fatalf("grammar missing enum literal %q:\n%s", lit, g)
		}
	}
}

func TestSchemaToGBNFUnsupported(t *testing.T) {
	_, err := SchemaToGBNF(map[string]any{"type": "flobnar"})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestUnionToolCallGrammar(t *testing.T) {
	reg := NewRegistry(
		Entry{Tool: NewRead(t.TempDir()), RiskTier: RiskSafe},
		Entry{Tool: NewBash(t.TempDir()), RiskTier: RiskDangerous},
	)
	g, err := UnionToolCallGrammar(reg.Entries(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"read"`, `"bash"`, `"tool"`, `"args"`, "root ::="} {
		if !strings.Contains(g, want) {
			t.Fatalf("union grammar missing %q:\n%s", want, g)
		}
	}
}

func TestRegistryModeFilter(t *testing.T) {
	reg := NewRegistry(
		Entry{Tool: NewRead(t.TempDir()), RiskTier: RiskSafe, Modes: []string{ModeDiscussion, ModeAutopilot}},
		Entry{Tool: NewBash(t.TempDir()), RiskTier: RiskDangerous, Modes: []string{ModeAutopilot}},
	)
	disc := reg.Specs(ModeDiscussion)
	if len(disc) != 1 || disc[0].Function.Name != "read" {
		t.Fatalf("discussion specs = %+v", disc)
	}
	auto := reg.Specs(ModeAutopilot)
	if len(auto) != 2 {
		t.Fatalf("autopilot specs = %d tools, want 2", len(auto))
	}
}

func TestDangerousBlockedWithoutGuard(t *testing.T) {
	reg := NewRegistry(Entry{Tool: NewBash(t.TempDir()), RiskTier: RiskDangerous})
	_, err := reg.Execute(context.Background(), "bash", json.RawMessage(`{"command":"echo hi"}`))
	if err == nil || !strings.Contains(err.Error(), "m1-guard") {
		t.Fatalf("expected guard block, got %v", err)
	}
}

func TestGuardAllowsWhenSet(t *testing.T) {
	reg := NewRegistry(Entry{Tool: NewBash(t.TempDir()), RiskTier: RiskDangerous})
	reg.SetGuard(func(tool string, args json.RawMessage) error { return nil })
	out, err := reg.Execute(context.Background(), "bash", json.RawMessage(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("out = %q", out)
	}
}

func TestEditAndWrite(t *testing.T) {
	tmp := t.TempDir()
	w := NewWrite(tmp)
	e := NewEdit(tmp)
	ctx := context.Background()
	if _, err := w.Execute(ctx, json.RawMessage(`{"path":"a/b.txt","content":"hello world"}`)); err != nil {
		t.Fatal(err)
	}
	out, err := e.Execute(ctx, json.RawMessage(`{"path":"a/b.txt","old_string":"world","new_string":"cerveau"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(tmp, "a", "b.txt"))
	if string(data) != "hello cerveau" {
		t.Fatalf("content = %q (edit said %s)", data, out)
	}
	if _, err := e.Execute(ctx, json.RawMessage(`{"path":"a/b.txt","old_string":"nope","new_string":"x"}`)); err == nil {
		t.Fatal("expected not-found error")
	}
	if _, err := w.Execute(ctx, json.RawMessage(`{"path":"../escape.txt","content":"x"}`)); err == nil {
		t.Fatal("expected jail escape error")
	}
}

func TestGrep(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "x.go"), []byte("package x\nfunc FindMe() {}\n"), 0o644)
	os.MkdirAll(filepath.Join(tmp, ".git"), 0o755)
	os.WriteFile(filepath.Join(tmp, ".git", "ignored"), []byte("FindMe"), 0o644)
	g := NewGrep(tmp)
	out, err := g.Execute(context.Background(), json.RawMessage(`{"pattern":"FindMe"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "x.go:2") {
		t.Fatalf("out = %q", out)
	}
	if strings.Contains(out, ".git") {
		t.Fatalf("grep leaked into .git: %q", out)
	}
}
