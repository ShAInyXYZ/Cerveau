package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var grepSkipDirs = map[string]bool{
	".git": true, "node_modules": true, ".crv": true, "dist": true, "vendor": true,
}

const grepCapChars = 8000

type Grep struct {
	j jail
}

func NewGrep(workspaceRoot string) *Grep { return &Grep{j: newJail(workspaceRoot)} }

func (t *Grep) Name() string { return "grep" }

func (t *Grep) Description() string {
	return "Search file contents in the workspace with a regex pattern. Returns matching lines with file:line. " +
		"Pass glob (e.g. \"*.js\") to search only matching filenames, and path to scope to a subdirectory."
}

func (t *Grep) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string"},
			"path":    map[string]any{"type": "string", "description": "subdirectory to search, default root"},
			"glob":    map[string]any{"type": "string", "description": "only search files whose name matches this glob, e.g. *.js or *.go"},
		},
		"required": []string{"pattern"},
	}
}

func (t *Grep) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Pattern == "" {
		return "", fmt.Errorf("pattern required")
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("bad regex: %w", err)
	}
	base := t.j.root
	if a.Path != "" {
		base, err = t.j.resolve(a.Path)
		if err != nil {
			return "", err
		}
	}
	var sb strings.Builder
	total := 0
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
		if info.Size() > maxFileSize {
			return nil
		}
		if a.Glob != "" {
			if ok, _ := filepath.Match(a.Glob, info.Name()); !ok {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(t.j.root, path)
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				fmt.Fprintf(&sb, "%s:%d: %s\n", rel, i+1, strings.TrimSpace(line))
				total++
				if sb.Len() > grepCapChars {
					fmt.Fprintf(&sb, "...[capped at %d chars]", grepCapChars)
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != filepath.SkipAll {
		return "", walkErr
	}
	if total == 0 {
		where := "the whole workspace"
		if a.Path != "" {
			where = a.Path
		}
		if a.Glob != "" {
			where += " (files matching " + a.Glob + ")"
		}
		return fmt.Sprintf("no matches for /%s/ — searched %s. If you expected a hit, check the pattern (it is a regex) or widen the path/glob.", a.Pattern, where), nil
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}
