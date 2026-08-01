package rfx

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateArgsEnumsAndRequired(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"quality": map[string]any{"type": "string", "enum": []any{"draft", "final"}},
			"scene":   map[string]any{"type": "string"},
			"extra":   map[string]any{"type": "boolean"},
		},
		"required": []any{"quality"},
	}
	sets := GenerateArgs(params, 100)
	if len(sets) == 0 {
		t.Fatal("no sets generated")
	}
	// Every enum value must appear somewhere.
	seen := map[string]bool{}
	for _, s := range sets {
		if q, ok := s["quality"].(string); ok {
			seen[q] = true
		}
	}
	if !seen["draft"] || !seen["final"] {
		t.Fatalf("enum values not covered: %v", seen)
	}
	// First set is the minimal required-only run.
	if _, hasExtra := sets[0]["extra"]; hasExtra {
		t.Fatal("minimal run includes an optional param")
	}
	if _, hasScene := sets[0]["scene"]; hasScene {
		t.Fatal("minimal run includes an optional param")
	}
	// The injection probe rides in the string samples.
	var sawInjection bool
	for _, s := range sets {
		if v, ok := s["scene"].(string); ok && strings.Contains(v, "rm -rf") {
			sawInjection = true
		}
	}
	if !sawInjection {
		t.Fatal("injection probe missing from string samples")
	}
}

func TestGenerateArgsCapAndEmpty(t *testing.T) {
	if sets := GenerateArgs(nil, 100); len(sets) != 1 {
		t.Fatalf("nil params: %d sets, want 1 (the empty run)", len(sets))
	}
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
			"b": map[string]any{"type": "integer"},
		},
	}
	if sets := GenerateArgs(params, 3); len(sets) > 3 {
		t.Fatalf("cap not respected: %d sets", len(sets))
	}
}

func TestCheckContract(t *testing.T) {
	c := Contract{MaxMs: 100, OutputRegex: "^ok", MustNotContain: []string{"panic"}}
	if err := CheckContract(c, "ok deployed", 50*time.Millisecond, nil); err != nil {
		t.Fatalf("green run failed contract: %v", err)
	}
	if err := CheckContract(c, "ok", 200*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "max_ms") {
		t.Fatalf("timing violation missed: %v", err)
	}
	if err := CheckContract(c, "failed hard", 10*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("regex violation missed: %v", err)
	}
	if err := CheckContract(c, "ok but panic happened", 10*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("must_not_contain violation missed: %v", err)
	}
	// Zero contract = defaults only.
	if err := CheckContract(Contract{}, "anything", time.Hour, nil); err != nil {
		t.Fatalf("zero contract rejected a run: %v", err)
	}
}
