package tools

import (
	"strings"
	"testing"
)

func TestGBNFRuleNamesHaveNoUnderscores(t *testing.T) {
	// llama.cpp rejects underscores in rule names ("failed to parse grammar").
	// Property keys like open_loops / promotion_candidates must be sanitized.
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"open_loops":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"promotion_candidates": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"open_loops", "promotion_candidates"},
	}
	g, err := SchemaToGBNF(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(g, "\n") {
		if i := strings.Index(line, " ::="); i > 0 {
			name := line[:i]
			if strings.Contains(name, "_") {
				t.Fatalf("rule name has underscore (llama.cpp will reject): %q", name)
			}
		}
	}
}
