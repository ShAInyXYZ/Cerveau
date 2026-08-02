package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// FileProbe is the ground truth for one declared plan file.
type FileProbe struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes"`
}

// probeFiles reports which workspace-relative paths actually exist. Planner
// needs this because checkpoint events only appear when a step COMPLETES —
// a step killed by a guard leaves real files behind and no event at all, so
// the event log alone under-reports progress.
//
// Containment mirrors the file-tool jail: a path that resolves outside the
// workspace is never stat'd, it is simply reported as absent.
func probeFiles(workspace string, paths []string) []FileProbe {
	root, err := filepath.Abs(workspace)
	if err != nil {
		root = workspace
	}
	out := make([]FileProbe, 0, len(paths))
	for _, p := range paths {
		fp := FileProbe{Path: p}
		full := filepath.Join(root, filepath.Clean("/"+p))
		if full == root || strings.HasPrefix(full, root+string(filepath.Separator)) {
			if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
				fp.Exists = true
				fp.Bytes = fi.Size()
			}
		}
		out = append(out, fp)
	}
	return out
}

// ProbeFiles serves the panel's self-check: POST {paths:[...]} against the
// ACTIVE workspace. Read-only — it stats, never opens.
func (a *API) ProbeFiles(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "paths required"})
		return
	}
	if len(body.Paths) > 200 {
		body.Paths = body.Paths[:200]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace": a.cfg.Workspace,
		"files":     probeFiles(a.cfg.Workspace, body.Paths),
	})
}
