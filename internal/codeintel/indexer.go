package codeintel

import (
	"context"
	"os"
	"path/filepath"
)

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".crv": true, "dist": true,
	"vendor": true, "target": true, "__pycache__": true, ".next": true,
}

const maxParseSize = 1 << 20

type Indexer struct {
	store *Store
	root  string
}

func NewIndexer(store *Store, workspaceRoot string) *Indexer {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		abs = workspaceRoot
	}
	return &Indexer{store: store, root: abs}
}

type IndexReport struct {
	Parsed  int `json:"parsed"`
	Skipped int `json:"skipped"`
	Removed int `json:"removed"`
	Errors  int `json:"errors"`
}

func (ix *Indexer) Index(ctx context.Context) (IndexReport, error) {
	var rep IndexReport
	known, err := ix.store.FileMTimes(ctx)
	if err != nil {
		return rep, err
	}
	seen := map[string]bool{}
	err = filepath.Walk(ix.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			rep.Errors++
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] && path != ix.root {
				return filepath.SkipDir
			}
			return nil
		}
		lang := langOf(path)
		if lang == "" || info.Size() > maxParseSize {
			return nil
		}
		rel, err := filepath.Rel(ix.root, path)
		if err != nil {
			return nil
		}
		seen[rel] = true
		mtime := info.ModTime().Unix()
		size := info.Size()
		if st, ok := known[rel]; ok && st.MTime == mtime && st.Size == size {
			rep.Skipped++
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			rep.Errors++
			return nil
		}
		var symbols []Symbol
		var calls []Call
		if lang == "go" {
			symbols, calls, err = extractGo(rel, src)
		} else {
			symbols, calls, err = extractRegex(rel, lang, src)
		}
		if err != nil {
			rep.Errors++
			return nil
		}
		if err := ix.store.ReplaceFile(ctx, rel, lang, mtime, size, symbols, calls); err != nil {
			rep.Errors++
			return nil
		}
		rep.Parsed++
		return nil
	})
	if err != nil {
		return rep, err
	}
	for path := range known {
		if !seen[path] {
			ix.store.RemoveFile(ctx, path)
			rep.Removed++
		}
	}
	return rep, nil
}

func (ix *Indexer) ReindexOnEdit(ctx context.Context, rel string) error {
	full := filepath.Join(ix.root, rel)
	info, err := os.Stat(full)
	if err != nil {
		ix.store.RemoveFile(ctx, rel)
		return nil
	}
	src, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	lang := langOf(rel)
	var symbols []Symbol
	var calls []Call
	if lang == "go" {
		symbols, calls, err = extractGo(rel, src)
	} else {
		symbols, calls, err = extractRegex(rel, lang, src)
	}
	if err != nil {
		return err
	}
	return ix.store.ReplaceFile(ctx, rel, lang, info.ModTime().Unix(), info.Size(), symbols, calls)
}
