package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Remote workspace picking.
//
// The desktop opens a native folder dialog on the machine, which is useless
// from a phone: the window appears on a screen the user isn't looking at and
// blocks until someone clicks it. So the panel gets a small directory
// browser instead — the same UI works on both, and the phone stops depending
// on a dialog it cannot see.
//
// This is deliberately NARROW. It lists directory NAMES only: no file
// contents, no file names, nothing above the user's home, and nothing
// reachable by escaping through `..` or a symlink. Picking a workspace needs
// exactly that and no more.

type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// browseRoot is the ceiling for remote browsing: the user's home.
func browseRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return home
}

// listDirs returns the sub-directories of `path`, refusing anything that
// resolves outside `root` (including via symlinks).
func listDirs(root, path string) ([]dirEntry, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	// resolve the root itself, so a symlinked home still compares correctly
	if r, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = r
	}

	target := path
	if target == "" {
		target = rootAbs
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootAbs, target)
	}
	target = filepath.Clean(target)

	// follow symlinks BEFORE the containment check — otherwise a link inside
	// the root is a door straight out of it
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	if target != rootAbs && !strings.HasPrefix(target, rootAbs+string(filepath.Separator)) {
		return nil, fmt.Errorf("path is outside the browsable root")
	}

	items, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	out := make([]dirEntry, 0, len(items))
	for _, it := range items {
		if !it.IsDir() {
			continue // directories only — this is a folder picker, not a file browser
		}
		if strings.HasPrefix(it.Name(), ".") {
			continue // dotfolders are noise when choosing a workspace
		}
		out = append(out, dirEntry{Name: it.Name(), Path: filepath.Join(target, it.Name())})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// handleFSList serves GET /api/fs/list?path=… for the workspace picker.
func (a *API) handleFSList(w http.ResponseWriter, r *http.Request) {
	root := browseRoot()
	path := r.URL.Query().Get("path")
	if path == "" {
		path = root
	}
	entries, err := listDirs(root, path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// where we are, and where "up" goes (never above the root)
	cur := filepath.Clean(path)
	parent := filepath.Dir(cur)
	if cur == root || !strings.HasPrefix(parent, root) {
		parent = ""
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"root":    root,
		"path":    cur,
		"parent":  parent,
		"entries": entries,
	})
}

// FSList is the exported handler for the workspace picker.
func (a *API) FSList(w http.ResponseWriter, r *http.Request) { a.handleFSList(w, r) }
