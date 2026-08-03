package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The guidebook is the core's book of self-fixes: trivially-mechanical tool
// failures the harness repairs itself instead of surfacing an error card and
// burning a model iteration on it. A port is busy? Try the next one. A regex
// doesn't parse? Search for it literally. The model only ever sees the
// repaired result, prefixed with a note of what was fixed, so nothing is
// hidden — it just never has to solve solved problems.
//
// Rules live HERE, in the core, not in the prompt: prompt advice is a
// suggestion the model may ignore; a guidebook entry always runs. Add new
// rules by appending to the table — each one is a matcher plus a repair.
//
// Boundaries: a rule must be (1) mechanical — no judgment call the user could
// disagree with, (2) loss-free — never discards what the model asked for,
// only adjusts the how, (3) disclosed — the note says exactly what changed.
// Real errors (missing files, failed matches, denied guards) are NOT
// repairable: the model must see those and think.

type fixRule struct {
	tool   string                     // tool this rule applies to
	match  func(errMsg string) bool   // does this failure fit?
	repair func(args json.RawMessage) (json.RawMessage, string, bool)
}

// maxAutoFixes bounds the repair-retry loop per call: a port scan across a
// crowded range converges or gives up quickly, and a rule that keeps failing
// can never loop forever.
const maxAutoFixes = 8

var guidebook = []fixRule{
	// serve: requested port is taken → try the next one. The model does not
	// care WHICH port it gets, it cares that a server starts; the URL in the
	// result tells everyone where it actually landed.
	{
		tool:  "serve",
		match: func(e string) bool { return strings.Contains(e, "address already in use") },
		repair: func(args json.RawMessage) (json.RawMessage, string, bool) {
			var a map[string]any
			if json.Unmarshal(args, &a) != nil {
				return nil, "", false
			}
			port := 8000
			if p, ok := a["port"].(float64); ok && p > 0 {
				port = int(p)
			}
			a["port"] = port + 1
			out, err := json.Marshal(a)
			if err != nil {
				return nil, "", false
			}
			return out, fmt.Sprintf("port %d was busy — used %d instead", port, port+1), true
		},
	},
	// grep: the pattern is not a valid regex → search for it as a literal
	// string. Small models write `foo(bar` meaning the literal text; failing
	// the whole search over regex syntax helps no one.
	{
		tool:  "grep",
		match: func(e string) bool { return strings.Contains(e, "bad regex") },
		repair: func(args json.RawMessage) (json.RawMessage, string, bool) {
			var a map[string]any
			if json.Unmarshal(args, &a) != nil {
				return nil, "", false
			}
			pat, _ := a["pattern"].(string)
			if pat == "" {
				return nil, "", false
			}
			quoted := regexp.QuoteMeta(pat)
			if quoted == pat {
				return nil, "", false // quoting changed nothing — a retry would fail identically
			}
			a["pattern"] = quoted
			out, err := json.Marshal(a)
			if err != nil {
				return nil, "", false
			}
			return out, "the pattern was not a valid regex — searched for it as literal text instead", true
		},
	},
}

// guidebookRepair looks a failure up in the guidebook. It returns the repaired
// args, a human-readable note of what changed, and whether a repair applies.
func guidebookRepair(tool string, args json.RawMessage, errMsg string) (json.RawMessage, string, bool) {
	for _, r := range guidebook {
		if r.tool == tool && r.match(errMsg) {
			return r.repair(args)
		}
	}
	return nil, "", false
}
