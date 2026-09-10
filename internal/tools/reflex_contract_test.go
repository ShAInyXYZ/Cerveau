package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cerveau/internal/rfx"
)

func TestExecApprovalIsHostOwned(t *testing.T) {
	t.Setenv("CRV_RFX_HUMAN_APPROVED", "1")
	def := execDef("approval-proof", "/usr/bin/env")
	def.Card.Env = []string{"CRV_RFX_HUMAN_APPROVED"}
	reg := testReg()
	reg.SetWorkspace(t.TempDir())
	reg.AddReflexes([]rfx.Reflex{def})
	for _, approved := range []bool{false, true} {
		ctx := context.Background()
		if approved {
			ctx = WithHumanApproval(ctx)
		}
		out, err := reg.ExecuteMode(ctx, def.Name, json.RawMessage(`{}`), "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "CRV_RFX_HUMAN_APPROVED=1") != approved {
			t.Fatalf("approval provenance lost: approved=%v output=%q", approved, out)
		}
	}
}

func TestExecCaptureIsBounded(t *testing.T) {
	def := execDef("large-output", "/usr/bin/head", "-c", "3000000", "/dev/zero")
	out, err := runExec(t, def, nil)
	if err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("oversized output must fail visibly, len=%d err=%v", len(out), err)
	}
	if len(out) > 2<<20 {
		t.Fatalf("unbounded output retained: %d", len(out))
	}
}

func TestReflexNestedArgumentContract(t *testing.T) {
	schema := map[string]any{
		"type": "object", "properties": map[string]any{
			"files": map[string]any{"type": "array", "minItems": 1, "maxItems": 2,
				"items": map[string]any{"type": "string", "minLength": 1}},
			"check": map[string]any{"type": "object", "required": []any{"kind"},
				"properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []any{"visible", "text"}}}},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
		}, "required": []any{"files", "check", "limit"},
	}
	for _, raw := range []string{
		`{"files":[3],"check":{"kind":"visible"},"limit":5}`,
		`{"files":[],"check":{"kind":"visible"},"limit":5}`,
		`{"files":["a"],"check":{},"limit":5}`,
		`{"files":["a"],"check":{"kind":"other"},"limit":5}`,
		`{"files":["a"],"check":{"kind":"visible"},"limit":99}`,
	} {
		var args map[string]any
		json.Unmarshal([]byte(raw), &args)
		if err := validateReflexArgs(schema, args); err == nil {
			t.Errorf("invalid nested args accepted: %s", raw)
		}
	}
	var args map[string]any
	json.Unmarshal([]byte(`{"files":["a b.md"],"check":{"kind":"visible"},"limit":5}`), &args)
	if err := validateReflexArgs(schema, args); err != nil {
		t.Fatal(err)
	}
}

func TestNestedReflexCannotWidenParentCard(t *testing.T) {
	called := false
	reg := testReg(Entry{Tool: &stubTool{name: "apply_patch", fn: func(json.RawMessage, string) (string, error) { called = true; return "written", nil }}})
	inner := pipeline("inner", rfx.Step{Tool: "apply_patch", Args: map[string]any{"patch": "test"}})
	inner.Card = rfx.DefaultCard()
	outer := pipeline("outer", rfx.Step{Tool: "inner", Args: map[string]any{}})
	outer.Card = rfx.Card{FS: []string{"none"}, Network: []string{"none"}}
	reg.AddReflexes([]rfx.Reflex{inner, outer})
	if _, err := runReflex(t, reg, "outer", nil, ""); err == nil || called {
		t.Fatal("parent fs:none lost through nested reflex")
	}
}

func TestReflexOpenNestedObjectIsNotAnEmptyClosedSchema(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"patch": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"meta":  map[string]any{"type": "object"},
			"nodes": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		}},
	}}
	var args map[string]any
	json.Unmarshal([]byte(`{"patch":{"meta":{"title":"Architecture"},"nodes":[{"id":"runner","kind":"worker"}]}}`), &args)
	if err := validateReflexArgs(schema, args); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(`{"patch":{"remove":["runner"]}}`), &args)
	if err := validateReflexArgs(schema, args); err == nil {
		t.Fatal("closed patch accepted unknown removal field")
	}
}

func TestReflexEnumStillHonorsOtherConstraints(t *testing.T) {
	if err := checkArgType("name", map[string]any{"type": "string", "enum": []any{"long"}, "maxLength": 2}, "long"); err == nil {
		t.Fatal("enum bypassed length constraint")
	}
}

func TestNestedExecCannotWidenSubprocessOrEnvironment(t *testing.T) {
	t.Setenv("RFX_TEST_PRIVATE", "private-marker")
	for _, subprocess := range []bool{false, true} {
		reg := testReg()
		reg.SetWorkspace(t.TempDir())
		child := execDef("nested-env", "/usr/bin/env")
		child.Card.Env = []string{"RFX_TEST_PRIVATE"}
		parent := pipeline("parent", rfx.Step{Tool: "nested-env", Args: map[string]any{}})
		parent.Card = rfx.DefaultCard()
		parent.Card.Subprocess = subprocess
		if errs := reg.AddReflexes([]rfx.Reflex{child, parent}); len(errs) > 0 {
			t.Fatal(errs)
		}
		out, err := runReflex(t, reg, "parent", nil, "")
		if !subprocess && err == nil {
			t.Fatal("parent denied subprocess but child executed")
		}
		if subprocess && err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "private-marker") {
			t.Fatal("nested exec widened parent environment")
		}
	}
}
