package loop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const recoveryCoverageTracks = 64

// Credit measures newly inspected current source, not a successful diagnosis
// or repair. It is deliberately independent of the bounded excerpt list: a
// forgotten display excerpt must never make old coverage new again.
type recoveryCoverage struct {
	workspace string
	tracks    []recoveryCoverageTrack
}

type recoveryCoverageTrack struct {
	path, sha        string
	info             os.FileInfo
	seeded, observed []uint64
}

func newRecoveryCoverage(workspace string) *recoveryCoverage {
	return &recoveryCoverage{workspace: workspace}
}

// The caller establishes that this is an actual successful built-in read,
// paired to its journal call. This helper additionally validates the receipt
// and body against jailed current bytes. Unversioned/invalid/stale evidence
// earns no credit. Each byte occupies two bits at most: the worst case across
// 64 full 1 MiB files is 16 MiB, without unbounded interval fragmentation.
func (c *recoveryCoverage) observeRead(out string, seeded bool) {
	header, _, ok := strings.Cut(out, "\n")
	if !ok || !strings.HasPrefix(header, "[read ") || !strings.HasSuffix(header, "]") {
		return
	}
	var meta struct {
		Path, SHA256 string
		Start        *int  `json:"start_offset"`
		End          *int  `json:"end_offset"`
		Total        *int  `json:"total_bytes"`
		From         *int  `json:"from_line"`
		To           *int  `json:"to_line"`
		PartialStart *bool `json:"partial_start"`
		PartialEnd   *bool `json:"partial_end"`
		EOF          *bool `json:"eof"`
	}
	if json.Unmarshal([]byte(header[6:len(header)-1]), &meta) != nil || !filepath.IsLocal(meta.Path) || len(meta.SHA256) != 64 || meta.Start == nil || meta.End == nil || meta.Total == nil || meta.From == nil || meta.To == nil || meta.PartialStart == nil || meta.PartialEnd == nil || meta.EOF == nil {
		return
	}
	start, end := *meta.Start, *meta.End
	if start < 0 || end <= start || end > recoveryFileLimit || *meta.Total < end || *meta.Total > recoveryFileLimit {
		return
	}
	path := filepath.Clean(meta.Path)
	data, info, ok := coverageCurrentSource(c.workspace, path)
	if !ok || len(data) != *meta.Total || end > len(data) || fmt.Sprintf("%x", sha256.Sum256(data)) != meta.SHA256 {
		return
	}
	if (start < len(data) && !utf8.RuneStart(data[start])) || (end < len(data) && !utf8.RuneStart(data[end])) {
		return
	}
	source := string(data)
	from, to := 1+strings.Count(source[:start], "\n"), 1+strings.Count(source[:end-1], "\n")
	partialStart := start > 0 && source[start-1] != '\n'
	partialEnd := end < len(source) && source[end-1] != '\n'
	if *meta.From != from || *meta.To != to || *meta.PartialStart != partialStart || *meta.PartialEnd != partialEnd || *meta.EOF != (end == len(source)) {
		return
	}
	view, decoded := decodedRecoveryRead(out)
	if !decoded || view.FromLine != from || view.Content != source[start:end] {
		return
	}
	var track *recoveryCoverageTrack
	for i := range c.tracks {
		t := &c.tracks[i]
		if t.sha == meta.SHA256 && (t.path == path || os.SameFile(t.info, info)) {
			track = t
			// Use the latest validated alias for revalidation. Equal-content
			// atomic replacement at the same path preserves prior coverage.
			track.path, track.info = path, info
			break
		}
	}
	if track == nil {
		if len(c.tracks) >= recoveryCoverageTracks {
			return
		}
		words := (len(data) + 63) / 64
		c.tracks = append(c.tracks, recoveryCoverageTrack{path: path, sha: meta.SHA256, info: info, seeded: make([]uint64, words), observed: make([]uint64, words)})
		track = &c.tracks[len(c.tracks)-1]
	}
	if seeded {
		markRecoveryCoverage(track.seeded, start, end)
	} else {
		markRecoveryCoverage(track.observed, start, end)
	}
}

func markRecoveryCoverage(bitmap []uint64, start, end int) {
	for word := start / 64; word <= (end-1)/64; word++ {
		lo, hi := max(0, start-word*64), min(64, end-word*64)
		mask := ^uint64(0) << lo
		if hi < 64 {
			mask &= (uint64(1) << hi) - 1
		}
		bitmap[word] |= mask
	}
}

// Revalidate at the grant checkpoint, not merely when the read was observed.
// A historical or externally changed version cannot purchase more iterations.
// Like any read-time observation this does not lock out later operator edits;
// it never relaxes version-bound edit or verification requirements.
func (c *recoveryCoverage) freshBytes() int {
	n := 0
	for _, track := range c.tracks {
		data, info, ok := coverageCurrentSource(c.workspace, track.path)
		if !ok || !os.SameFile(track.info, info) || fmt.Sprintf("%x", sha256.Sum256(data)) != track.sha {
			continue
		}
		for i, seen := range track.observed {
			n += bits.OnesCount64(seen &^ track.seeded[i])
		}
	}
	return n
}

// OpenRoot enforces path containment even through directory symlinks. Final
// symlinks, non-regular files, oversized files and unstable reads are refused.
// No lexical prefix check or ambient filesystem read can escape the jail.
func coverageCurrentSource(workspace, path string) ([]byte, os.FileInfo, bool) {
	if !filepath.IsLocal(path) {
		return nil, nil, false
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, nil, false
	}
	defer root.Close()
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > recoveryFileLimit {
		return nil, nil, false
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, nil, false
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > recoveryFileLimit || !os.SameFile(info, before) {
		return nil, nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, recoveryFileLimit+1))
	if err != nil || len(data) > recoveryFileLimit || !utf8.Valid(data) {
		return nil, nil, false
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || before.ModTime() != after.ModTime() || !os.SameFile(before, after) || int64(len(data)) != after.Size() {
		return nil, nil, false
	}
	return data, after, true
}
