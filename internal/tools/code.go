package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cerveau/internal/codeintel"
)

type FileMap struct {
	store *codeintel.Store
}

func NewFileMap(s *codeintel.Store) *FileMap { return &FileMap{store: s} }

func (t *FileMap) Name() string { return "file_map" }

func (t *FileMap) Description() string {
	return "Compact structural map of the workspace: files with their symbol counts. Start here before reading files."
}

func (t *FileMap) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *FileMap) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	rows, err := t.store.FileMap(ctx)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "code graph empty — run a reindex", nil
	}
	dirs := map[string][]string{}
	for _, r := range rows {
		dir := r.Path
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			dir = dir[:i]
		} else {
			dir = "."
		}
		dirs[dir] = append(dirs[dir], fmt.Sprintf("%s(%d)", fileBase(r.Path), r.Count))
	}
	keys := make([]string, 0, len(dirs))
	for k := range dirs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, d := range keys {
		fmt.Fprintf(&sb, "%s/ %s\n", d, strings.Join(dirs[d], " "))
	}
	out := sb.String()
	if len(out) > 6000 {
		out = out[:6000] + "\n...[truncated]"
	}
	return strings.TrimRight(out, "\n"), nil
}

func fileBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

type FindSymbol struct {
	store *codeintel.Store
}

func NewFindSymbol(s *codeintel.Store) *FindSymbol { return &FindSymbol{store: s} }

func (t *FindSymbol) Name() string { return "find_symbol" }

func (t *FindSymbol) Description() string {
	return "Locate a function/type/class definition by name. Returns file:line with signature. Cheaper than grep."
}

func (t *FindSymbol) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []string{"name"},
	}
}

func (t *FindSymbol) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
		return "", fmt.Errorf("name required")
	}
	syms, err := t.store.FindSymbols(ctx, a.Name, 20)
	if err != nil {
		return "", err
	}
	if len(syms) == 0 {
		return fmt.Sprintf("no symbol %q in the code graph (maybe reindex?)", a.Name), nil
	}
	var sb strings.Builder
	for _, s := range syms {
		fmt.Fprintf(&sb, "%s:%d [%s] %s\n", s.File, s.Line, s.Kind, s.Signature)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

type FindReferences struct {
	store *codeintel.Store
}

func NewFindReferences(s *codeintel.Store) *FindReferences { return &FindReferences{store: s} }

func (t *FindReferences) Name() string { return "find_references" }

func (t *FindReferences) Description() string {
	return "Find callers of a symbol across the workspace. Returns file:line and the calling function."
}

func (t *FindReferences) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required": []string{"name"},
	}
}

func (t *FindReferences) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
		return "", fmt.Errorf("name required")
	}
	refs, err := t.store.References(ctx, a.Name, 30)
	if err != nil {
		return "", err
	}
	if len(refs) == 0 {
		return fmt.Sprintf("no references to %q in the code graph", a.Name), nil
	}
	var sb strings.Builder
	for _, r := range refs {
		fmt.Fprintf(&sb, "%s:%d in %s\n", r.CallerFile, r.Line, r.CallerSymbol)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

type OutlineFile struct {
	store *codeintel.Store
}

func NewOutlineFile(s *codeintel.Store) *OutlineFile { return &OutlineFile{store: s} }

func (t *OutlineFile) Name() string { return "outline_file" }

func (t *OutlineFile) Description() string {
	return "List the symbols of one file in order: kinds, signatures, lines. Read a file's shape without reading it."
}

func (t *OutlineFile) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []string{"path"},
	}
}

func (t *OutlineFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Path == "" {
		return "", fmt.Errorf("path required")
	}
	syms, err := t.store.FileSymbols(ctx, a.Path)
	if err != nil {
		return "", err
	}
	if len(syms) == 0 {
		return fmt.Sprintf("no symbols for %q in the code graph (maybe reindex?)", a.Path), nil
	}
	var sb strings.Builder
	for _, s := range syms {
		fmt.Fprintf(&sb, "%4d [%s] %s\n", s.Line, s.Kind, s.Signature)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}
