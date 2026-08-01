package rfx

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// GenerateArgs deterministically produces argument sets from a params schema
// (spec §6): every enum value, boundary numbers, booleans, empty/unicode/
// injection-shaped strings, empty and filled arrays. Deterministic — fuzz
// that flakes is a siren nobody trusts. Capped at max sets.
func GenerateArgs(params map[string]any, maxSets int) []map[string]any {
	if maxSets <= 0 {
		maxSets = 100
	}
	props, _ := params["properties"].(map[string]any)
	if len(props) == 0 {
		return []map[string]any{{}}
	}
	required := map[string]bool{}
	if req, ok := params["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}

	// Stable param order: required first, then optional, alphabetical within.
	names := sortedKeys(props, required)

	sets := []map[string]any{{}}
	for _, name := range names {
		samples := sampleValues(props[name])
		var next []map[string]any
		for _, set := range sets {
			for _, sv := range samples {
				cp := copyMap(set)
				cp[name] = sv
				next = append(next, cp)
			}
		}
		sets = truncateSets(next, maxSets)
	}
	// One minimal run: required params only (optionals omitted entirely).
	if len(required) > 0 && len(required) < len(props) {
		minimal := map[string]any{}
		for name := range required {
			minimal[name] = sampleValues(props[name])[0]
		}
		sets = append([]map[string]any{minimal}, sets...)
	}
	return truncateSets(sets, maxSets)
}

func sortedKeys(props map[string]any, required map[string]bool) []string {
	var req, opt []string
	for k := range props {
		if required[k] {
			req = append(req, k)
		} else {
			opt = append(opt, k)
		}
	}
	sortStrings(req)
	sortStrings(opt)
	return append(req, opt...)
}

func sortStrings(s []string) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func truncateSets(sets []map[string]any, max int) []map[string]any {
	if len(sets) > max {
		return sets[:max]
	}
	return sets
}

// sampleValues returns the probe values for one param schema. The injection
// string is deliberate: a reflex that mishandles it should fail HERE.
func sampleValues(schema any) []any {
	sm, _ := schema.(map[string]any)
	if enums, ok := sm["enum"].([]any); ok && len(enums) > 0 {
		out := make([]any, len(enums))
		copy(out, enums)
		return out
	}
	switch typ, _ := sm["type"].(string); typ {
	case "integer":
		return []any{float64(0), float64(1), float64(-1), float64(999999)}
	case "number":
		return []any{float64(0), float64(1.5), float64(-2.5)}
	case "boolean":
		return []any{true, false}
	case "array":
		itemSamples := sampleValues(sm["items"])
		var filled []any
		if len(itemSamples) > 0 {
			filled = []any{itemSamples[0]}
		}
		return []any{[]any{}, filled}
	case "object":
		return []any{map[string]any{}}
	default: // string (and unspecified)
		return []any{"x", "", `"; rm -rf ~; #`, "héllo wörld", strings.Repeat("a", 256)}
	}
}

// CheckContract verifies one fuzz run against the contract (spec §6).
// A nil/zero contract applies the defaults: completes, output sane.
func CheckContract(c Contract, output string, elapsed time.Duration, runErr error) error {
	if runErr != nil {
		return fmt.Errorf("run failed: %w", runErr)
	}
	if c.MaxMs > 0 && elapsed > time.Duration(c.MaxMs)*time.Millisecond {
		return fmt.Errorf("contract: took %s, max_ms is %d", elapsed.Round(time.Millisecond), c.MaxMs)
	}
	if c.OutputRegex != "" {
		re, err := regexp.Compile(c.OutputRegex)
		if err != nil {
			return fmt.Errorf("contract: bad output_regex %q: %w", c.OutputRegex, err)
		}
		if !re.MatchString(output) {
			return fmt.Errorf("contract: output does not match %q", c.OutputRegex)
		}
	}
	for _, bad := range c.MustNotContain {
		if strings.Contains(output, bad) {
			return fmt.Errorf("contract: output contains forbidden %q", bad)
		}
	}
	return nil
}
