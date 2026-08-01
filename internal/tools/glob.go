package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Glob finds files by name pattern — the discovery tool read/grep can't be:
// read needs an exact path, grep searches contents, file_map only covers
// indexed code languages. Safe tier, every mode.
type Glob struct {
	j jail
}

func NewGlob(workspaceRoot string) *Glob { return &Glob{j: newJail(workspaceRoot)} }

func (t *Glob) Name() string { return "glob" }

func (t *Glob) Description() string {
	return "Find files by name pattern (e.g. \"**/*.go\", \"*.yaml\", \"src/**/test_*\"). Returns relative paths, sorted. Use this to locate files before reading them."
}

func (t *Glob) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string", "description": "glob pattern: * one segment, ** any depth, ? one char"},
			"path":    map[string]any{"type": "string", "description": "subdirectory to search, default root"},
		},
		"required": []string{"pattern"},
	}
}

const globMaxResults = 500

func (t *Glob) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Pattern == "" {
		return "", fmt.Errorf("pattern required")
	}
	if err := validateGlobPattern(a.Pattern); err != nil {
		return "", err
	}
	base := t.j.root
	var err error
	if a.Path != "" {
		base, err = t.j.resolve(a.Path)
		if err != nil {
			return "", err
		}
	}

	var matches []string
	walkErr := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if grepSkipDirs[info.Name()] && path != base {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(t.j.root, path)
		// The pattern applies WITHIN the searched dir (glob-as-cwd semantics);
		// results stay root-relative so the model can read them directly.
		matchRel, _ := filepath.Rel(base, path)
		if globMatch(a.Pattern, filepath.ToSlash(matchRel)) {
			matches = append(matches, rel)
			if len(matches) >= globMaxResults {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != filepath.SkipAll {
		return "", walkErr
	}
	if len(matches) == 0 {
		return "no matches", nil
	}
	sort.Strings(matches)
	out := strings.Join(matches, "\n")
	if len(matches) >= globMaxResults {
		out += fmt.Sprintf("\n…[capped at %d results — narrow the pattern]", globMaxResults)
	}
	return out, nil
}

func validateGlobPattern(p string) error {
	if strings.Contains(p, "..") {
		return fmt.Errorf("pattern must not contain '..'")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "**" {
			continue
		}
		if strings.Contains(seg, "*") && seg != "*" && strings.Count(seg, "*") > 1 && strings.Contains(seg, "**") {
			return fmt.Errorf("bad pattern segment %q", seg)
		}
	}
	return nil
}

// globMatch: * matches within a segment, ** matches any number of segments,
// ? matches one char. Matching is on slash-separated relative paths.
func globMatch(pattern, name string) bool {
	p := strings.Split(pattern, "/")
	n := strings.Split(name, "/")
	return matchSegs(p, n)
}

func matchSegs(p, n []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			// ** consumes zero or more segments
			for i := 0; i <= len(n); i++ {
				if matchSegs(p[1:], n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 {
			return false
		}
		ok, err := filepath.Match(p[0], n[0])
		if err != nil || !ok {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}
