package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

const readCapChars = 8000

type Read struct {
	j jail

	// A small local model often ignores the offset hint and simply re-reads
	// the same path, getting the identical head until the loop guard kills
	// the turn. Remember where each path last stopped so an unqualified
	// repeat AUTO-ADVANCES instead of spinning. An explicit offset always
	// wins, and reaching EOF resets the cursor.
	readCursor
}

func NewRead(workspaceRoot string) *Read {
	return &Read{j: newJail(workspaceRoot), readCursor: readCursor{next: map[string]int{}}}
}

type readCursor struct {
	mu       sync.Mutex
	next     map[string]int
	versions map[string]string
}

type readCursorKey struct{}

// WithFreshReadCursor scopes automatic read continuation to one model window.
// A new chat turn or step attempt must not inherit another window's offsets.
// Registries may share Read pointers, so resetting the tool itself is unsafe.
func WithFreshReadCursor(ctx context.Context) context.Context {
	return context.WithValue(ctx, readCursorKey{}, &readCursor{next: map[string]int{}})
}

// rawPatchReadKey is private: only patch prevalidation requests raw source.
// Keeping this on the context preserves registry guards and the read jail;
// it is not a model-visible argument that can bypass the public output cap.
type rawPatchReadKey struct{}

func (t *Read) Name() string { return "read" }

func (t *Read) Description() string {
	return "Read a file from the workspace, with line numbers. Path is relative to the workspace root. " +
		"Use from_line/to_line to read just the part you care about instead of the whole file. " +
		"The [read] metadata reports actual coverage, SHA-256 and exact next_offset (UTF-8 bytes). " +
		"Follow next_offset, preserving to_line and expected_sha256, if a range needs more pages. " +
		"Large single lines are explicitly marked as fragments. Unqualified repeats auto-continue only while the file version is unchanged."
}

func (t *Read) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":            map[string]any{"type": "string", "description": "workspace-relative file path"},
			"from_line":       map[string]any{"type": "integer", "description": "first line to return (1-based). Use with to_line to read only the region you need"},
			"to_line":         map[string]any{"type": "integer", "description": "last line to return (1-based, inclusive)"},
			"offset":          map[string]any{"type": "integer", "description": "UTF-8 byte offset, normally the exact next_offset from read metadata. Can be combined with from_line/to_line for range continuation; unqualified repeats auto-continue."},
			"expected_sha256": map[string]any{"type": "string", "description": "Optional SHA-256 from an earlier read; refuses stale continuation if the file changed."},
		},
		"required": []string{"path"},
	}
}

func (t *Read) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path        string  `json:"path"`
		Offset      *int    `json:"offset"`
		FromLine    int     `json:"from_line"`
		ToLine      int     `json:"to_line"`
		ExpectedSHA *string `json:"expected_sha256"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Path == "" {
		return "", fmt.Errorf("path required")
	}
	full, err := t.j.resolve(a.Path)
	if err != nil {
		return "", err
	}
	if raw, _ := ctx.Value(rawPatchReadKey{}).(bool); raw {
		f, err := os.Open(full)
		if err != nil {
			return "", err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
		if err != nil {
			return "", err
		}
		if len(data) > maxFileSize {
			return "", fmt.Errorf("file too large for patch validation (max %d bytes); no edits applied", maxFileSize)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	full_ := string(data)
	version := sourceSHA256(full_)
	if err := checkSourceVersion(full_, a.ExpectedSHA); err != nil {
		return "", err
	}
	cursor, _ := ctx.Value(readCursorKey{}).(*readCursor)
	if cursor == nil {
		cursor = &t.readCursor
	}

	// A target range restricts the raw source bounds, not just its header.
	// Numbered output is paginated afterwards so no unseen line is advertised.
	starts := []int{0}
	for i := 0; i < len(full_); i++ {
		if full_[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	ranged := a.FromLine > 0 || a.ToLine > 0
	from, to := max(1, a.FromLine), a.ToLine
	if to <= 0 || to > len(starts) {
		to = len(starts)
	}
	if from > len(starts) {
		return "", fmt.Errorf("from_line %d is past the end; %s has %d lines (sha256 %s)", from, a.Path, len(starts), version)
	}
	if from > to {
		return "", fmt.Errorf("from_line must not exceed to_line")
	}
	start, limit := 0, len(full_)
	if ranged {
		start = starts[from-1]
		if to < len(starts) {
			limit = starts[to]
		}
	}
	off := start
	auto := false
	switch {
	case a.Offset != nil:
		off = max(start, *a.Offset)
	case !ranged:
		cursor.mu.Lock()
		if cursor.versions[full] == version {
			off = cursor.next[full]
		}
		cursor.mu.Unlock()
		auto = off > 0
	}
	if off > limit {
		return "", fmt.Errorf("offset %d is past the selected range end at byte %d (file has %d bytes)", off, limit, len(full_))
	}
	// Legacy explicit byte offsets may split a rune. Include that rune from
	// its start rather than corrupt UTF-8 or silently skip source bytes.
	for off > start && off < len(full_) && !utf8.RuneStart(full_[off]) {
		off--
	}
	firstLine := 1 + strings.Count(full_[:off], "\n")
	// The replay ingress limit applies to the WHOLE result, not only source.
	// Reserve a metadata/footer upper bound before choosing complete lines.
	// Include the JSON-encoded path: deeply nested or escaped names otherwise
	// silently push a correct receipt's source beyond replay's 8,000-byte cap.
	totalBytes, totalLines := len(full_), len(starts)
	upper := readMetadata{
		Path: a.Path, SHA256: version, FromLine: firstLine, ToLine: totalLines,
		StartOffset: off, EndOffset: totalBytes, TotalBytes: totalBytes, TotalLines: totalLines,
		NextOffset: &totalBytes, NextLine: &totalLines,
	}
	withCursor, _ := json.Marshal(upper)
	upper.NextOffset, upper.NextLine = nil, nil
	withoutCursor, _ := json.Marshal(upper)
	overhead := len("[read ") + max(len(withCursor), len(withoutCursor)) + len("]\n") + len(readContinuationNotice(totalBytes, to))
	if auto {
		overhead += len(readAutoNotice)
	}
	bodyBudget := readCapChars - overhead
	if bodyBudget < 32 {
		return "", fmt.Errorf("encoded path metadata exceeds the read output budget; use a shorter workspace-relative path alias")
	}
	body, end := readPage(full_, off, limit, firstLine, bodyBudget)
	lastLine := firstLine
	if end > off {
		lastLine = 1 + strings.Count(full_[:end-1], "\n")
	}
	meta := readMetadata{
		Path: a.Path, SHA256: version, FromLine: firstLine, ToLine: lastLine,
		StartOffset: off, EndOffset: end, TotalBytes: len(full_), TotalLines: len(starts),
		PartialStart: off > 0 && full_[off-1] != '\n',
		PartialEnd:   end < len(full_) && end > off && full_[end-1] != '\n', EOF: end == len(full_),
	}
	if end < limit {
		nextLine := 1 + strings.Count(full_[:end], "\n")
		meta.NextOffset, meta.NextLine = &end, &nextLine
	}
	if !ranged {
		cursor.mu.Lock()
		if end < limit {
			if cursor.versions == nil {
				cursor.versions = map[string]string{}
			}
			cursor.next[full], cursor.versions[full] = end, version
		} else {
			delete(cursor.next, full)
			delete(cursor.versions, full)
		}
		cursor.mu.Unlock()
	}
	header, _ := json.Marshal(meta)
	out := "[read " + string(header) + "]\n" + body
	if auto {
		out += readAutoNotice
	}
	if end < limit {
		out += readContinuationNotice(end, to)
	} else if end == len(full_) {
		out += "...[end of file]\n"
	}
	return out, nil
}

const readAutoNotice = "...[continuing the same file version from the previous byte cursor]\n"

func readContinuationNotice(end, to int) string {
	return fmt.Sprintf("...[slice incomplete: continue with offset:%d, to_line:%d and expected_sha256 from metadata. Line-number prefixes are not source.]\n", end, to)
}

// readMetadata is a stable machine-readable receipt. Offsets identify exactly
// the raw source bytes covered; end is exclusive. A null cursor means the
// requested region is complete, not necessarily that the file is complete.
type readMetadata struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	FromLine     int    `json:"from_line"`
	ToLine       int    `json:"to_line"`
	StartOffset  int    `json:"start_offset"`
	EndOffset    int    `json:"end_offset"`
	TotalBytes   int    `json:"total_bytes"`
	TotalLines   int    `json:"total_lines"`
	PartialStart bool   `json:"partial_start"`
	PartialEnd   bool   `json:"partial_end"`
	NextOffset   *int   `json:"next_offset"`
	NextLine     *int   `json:"next_line"`
	EOF          bool   `json:"eof"`
}

// readPage returns complete numbered lines within the presentation budget.
// Only a line that cannot fit by itself is fragmented, at a UTF-8 boundary.
func readPage(source string, start, limit, line, budget int) (string, int) {
	var body strings.Builder
	pos := start
	for pos < limit {
		end := limit
		if nl := strings.IndexByte(source[pos:limit], '\n'); nl >= 0 {
			end = pos + nl + 1
		}
		prefix := fmt.Sprintf("%d\t", line)
		noticeNL := 0
		if source[end-1] != '\n' {
			noticeNL = 1
		}
		if body.Len()+len(prefix)+end-pos+noticeNL > budget {
			if body.Len() > 0 {
				break
			}
			end = min(end, pos+budget-len(prefix)-1)
			for end > pos && end < len(source) && !utf8.RuneStart(source[end]) {
				end--
			}
			body.WriteString(prefix)
			body.WriteString(source[pos:end])
			body.WriteByte('\n')
			return body.String(), end
		}
		body.WriteString(prefix)
		body.WriteString(source[pos:end])
		if noticeNL != 0 {
			body.WriteByte('\n')
		}
		pos, line = end, line+1
	}
	// Empty source or the explicit trailing empty line is still a valid read.
	if start == limit {
		fmt.Fprintf(&body, "%d\t\n", line)
	}
	return body.String(), pos
}
