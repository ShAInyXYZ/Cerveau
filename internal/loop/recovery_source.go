package loop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A source ref names an immutable decoded field of a plan-scoped journal
// event. It is not an edit target, a backup, or an assertion that a proposed
// write was executed. Raw event IDs remain available through recovery_read id.
type recoverySource struct {
	Ref, EventID, Tool, File, Field, Content string
	ResultEventID, Execution, Scope          string
	FromLine                                 int
	PartialStart, PartialEnd                 bool
	FileSHA                                  string
}

type recoverySourcePayload struct {
	ID, Name string
	RunID    string `json:"run_id"`
	Args     map[string]json.RawMessage
	RawArgs  *string `json:"raw_args"`
	Output   *string
	OK       *bool
}

func sourceString(m map[string]json.RawMessage, key string) (string, bool) {
	var s string
	b, ok := m[key]
	if !ok || string(b) == "null" || json.Unmarshal(b, &s) != nil {
		return "", false
	}
	return s, true
}

func (r *recoveryReader) sourceViews() []recoverySource {
	ids := make([]string, 0, len(r.events))
	for id := range r.events {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []recoverySource
	type pendingCall struct {
		payload recoverySourcePayload
		sources []int
	}
	pending := map[string]pendingCall{}
	for _, id := range ids {
		var p recoverySourcePayload
		if json.Unmarshal(r.events[id], &p) != nil || p.Name == "recovery_read" {
			continue
		}
		key := p.RunID + "\x00" + p.Name + "\x00" + p.ID
		if p.Output == nil && (p.Args != nil || p.RawArgs != nil) {
			call := pendingCall{payload: p}
			path, _ := sourceString(p.Args, "path")
			add := func(field, file, content, scope string) {
				if !utf8.ValidString(content) {
					return
				}
				call.sources = append(call.sources, len(out))
				out = append(out, recoverySource{Ref: id + "#" + field, EventID: id, Tool: p.Name, File: file, Field: field, Content: content, Scope: scope, FromLine: 1, Execution: "unconfirmed"})
			}
			switch p.Name {
			case "write":
				if s, ok := sourceString(p.Args, "content"); ok {
					add("args.content", path, s, "proposed_file")
				}
			case "edit":
				for _, field := range []string{"old_string", "new_string"} {
					if s, ok := sourceString(p.Args, field); ok {
						add("args."+field, path, s, "edit_fragment")
					}
				}
			case "apply_patch":
				var edits []map[string]json.RawMessage
				if json.Unmarshal(p.Args["edits"], &edits) == nil {
					for i, edit := range edits {
						file, _ := sourceString(edit, "path")
						for _, field := range []string{"old_string", "new_string"} {
							if s, ok := sourceString(edit, field); ok {
								add(fmt.Sprintf("args.edits.%d.%s", i, field), file, s, "edit_fragment")
							}
						}
					}
				}
			}
			if p.ID != "" {
				// Dispatch is synchronous. A newer same-ID call supersedes an
				// orphaned call after interruption, including old unscoped logs.
				// raw_args is only a call marker; never duplicate it as source.
				pending[key] = call
			}
			continue
		}
		call, paired := pending[key]
		if p.Output == nil || p.ID == "" || !paired {
			continue
		}
		delete(pending, key)
		execution := "unconfirmed"
		if p.OK != nil {
			if *p.OK {
				execution = "tool_succeeded"
			} else {
				execution = "tool_failed"
			}
		}
		for _, i := range call.sources {
			out[i].Execution, out[i].ResultEventID = execution, id
		}
		if p.Name == "read" && p.OK != nil && *p.OK {
			if view, ok := decodedRecoveryRead(*p.Output); ok {
				view.Ref, view.EventID, view.Tool, view.Field = id+"#output.source", id, p.Name, "output.source"
				view.ResultEventID, view.Execution, view.Scope = id, execution, "captured_read"
				// The paired read's path is authoritative over presentation text.
				view.File, _ = sourceString(call.payload.Args, "path")
				out = append(out, view)
			}
		}
	}
	// Newest event first, stable field order within each event. This also
	// makes field-level pagination lossless for a multi-hunk patch event.
	sort.Slice(out, func(i, j int) bool {
		if out[i].EventID == out[j].EventID {
			return out[i].Field < out[j].Field
		}
		return out[i].EventID > out[j].EventID
	})
	return out
}

// Decode only numbered source rows. Historical range headers can overstate
// coverage, so actual sequential row numbers, not the header, define lines.
// Legacy truncation notices describe captured fragments, never file EOF.
func decodedRecoveryRead(output string) (recoverySource, bool) {
	v := recoverySource{}
	lines := strings.Split(output, "\n")
	var capturedBytes *int
	if len(lines) > 0 && strings.HasPrefix(lines[0], "[read ") && strings.HasSuffix(lines[0], "]") {
		var receipt struct {
			SHA256       string `json:"sha256"`
			PartialStart bool   `json:"partial_start"`
			PartialEnd   bool   `json:"partial_end"`
			StartOffset  *int   `json:"start_offset"`
			EndOffset    *int   `json:"end_offset"`
		}
		if json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(lines[0], "[read "), "]")), &receipt) == nil {
			v.FileSHA, v.PartialStart, v.PartialEnd = receipt.SHA256, receipt.PartialStart, receipt.PartialEnd
			if receipt.StartOffset != nil && receipt.EndOffset != nil && *receipt.StartOffset >= 0 && *receipt.EndOffset >= *receipt.StartOffset {
				n := *receipt.EndOffset - *receipt.StartOffset
				capturedBytes = &n
			}
		}
	}
	var source []string
	previous := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "...[range too large") || strings.HasPrefix(line, "...[slice ") {
			if capturedBytes == nil {
				v.PartialEnd = true
			}
			break
		}
		number, raw, hasTab := strings.Cut(line, "\t")
		n, err := strconv.Atoi(number)
		if !hasTab || err != nil || n < 1 {
			if previous != 0 {
				break
			}
			continue
		}
		if previous != 0 && n != previous+1 {
			break
		}
		if previous == 0 {
			v.FromLine = n
		}
		previous = n
		source = append(source, raw)
	}
	if len(source) == 0 {
		return v, false
	}
	v.Content = strings.Join(source, "\n")
	if capturedBytes != nil {
		// New read receipts distinguish a real final newline from the
		// renderer's presentation newline. Preserve exact captured bytes.
		candidate := v.Content + "\n"
		if *capturedBytes > len(candidate) {
			return v, false
		}
		v.Content = candidate[:*capturedBytes]
	}
	return v, utf8.ValidString(v.Content)
}

func (r *recoveryReader) readSource(ref string, offset int) (string, error) {
	for _, s := range r.sourceViews() {
		if s.Ref != ref {
			continue
		}
		if offset < 0 || offset > len(s.Content) {
			return "", fmt.Errorf("invalid decoded-source byte offset: valid range is 0..%d", len(s.Content))
		}
		if offset < len(s.Content) && !utf8.RuneStart(s.Content[offset]) {
			return "", fmt.Errorf("offset must be a UTF-8 character boundary")
		}
		out := sourceWindow(s, offset, 12000)
		if s.Scope == "proposed_file" && filepath.IsLocal(s.File) {
			r.addComparison(out, recoveryCopy{File: s.File, Blob: fmt.Sprintf("%x", sha256.Sum256([]byte(s.Content)))})
		}
		out["note"] = "Decoded historical source. tool_succeeded records only the paired tool outcome, not correctness or current-file identity. tool_failed may include partial batch effects. Offsets are in this decoded field; eof means end of captured evidence, not necessarily end of the original file."
		b, _ := json.Marshal(out)
		return string(b), nil
	}
	return "", fmt.Errorf("source ref is not in this plan's evidence; use a ref returned by query")
}

func sourceWindow(s recoverySource, start, capBytes int, minimumEnd ...int) map[string]any {
	end := min(start+capBytes, len(s.Content))
	for end < len(s.Content) && !utf8.RuneStart(s.Content[end]) {
		end--
	}
	if end < len(s.Content) {
		// Prefer complete lines; an oversized single line is an explicit
		// byte fragment, with an exact UTF-8-safe continuation offset.
		if nl := strings.LastIndexByte(s.Content[start:end], '\n'); nl >= 0 {
			candidate := start + nl + 1
			if len(minimumEnd) == 0 || candidate >= minimumEnd[0] {
				end = candidate
			}
		}
	}
	from := s.FromLine + strings.Count(s.Content[:start], "\n")
	to := from + strings.Count(strings.TrimSuffix(s.Content[start:end], "\n"), "\n")
	out := map[string]any{
		"ref": s.Ref, "event_id": s.EventID, "file": s.File, "field": s.Field, "tool": s.Tool, "scope": s.Scope, "execution": s.Execution,
		"source_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(s.Content))),
		"offset":        start, "next_offset": end, "total_bytes": len(s.Content), "eof": end == len(s.Content),
		"from_line": from, "to_line": to, "source": s.Content[start:end],
		"partial_start": (start > 0 && s.Content[start-1] != '\n') || (start == 0 && s.PartialStart),
		"partial_end":   (end < len(s.Content) && end > 0 && s.Content[end-1] != '\n') || (end == len(s.Content) && s.PartialEnd),
	}
	if s.ResultEventID != "" {
		out["result_event_id"] = s.ResultEventID
	}
	if s.FileSHA != "" {
		out["captured_file_sha256"] = s.FileSHA
	}
	return out
}

func (r *recoveryReader) searchSource(query, after, path string) (string, error) {
	if len(query) > 160 || strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query must be a nonempty literal of at most 160 bytes")
	}
	if path != "" && !filepath.IsLocal(path) {
		return "", fmt.Errorf("path must be workspace-relative")
	}
	sources := r.sourceViews()
	start := 0
	if after != "" {
		found := false
		for i, s := range sources {
			if s.Ref == after {
				start, found = i+1, true
				break
			}
			if s.EventID == after {
				start, found = i+1, true
			}
		}
		if !found {
			return "", fmt.Errorf("after must be a source ref or source event ID in this plan's evidence")
		}
	}
	hits := []map[string]any{}
	more, total := false, 0
	for _, s := range sources[start:] {
		if path != "" && filepath.Clean(s.File) != filepath.Clean(path) {
			continue
		}
		at := strings.Index(s.Content, query)
		if at < 0 {
			continue
		}
		begin := max(0, at-160)
		for begin > 0 && !utf8.RuneStart(s.Content[begin]) {
			begin--
		}
		// Start on a nearby complete line when possible, but never move
		// past the match if it occurs on an exceptionally long line.
		if nl := strings.IndexByte(s.Content[begin:at], '\n'); nl >= 0 {
			begin += nl + 1
		}
		// Complete-line preference must not cut a multiline matching query
		// short before its final line; partial flags remain explicit.
		hit := sourceWindow(s, begin, 600, at+len(query))
		hit["snippet"] = hit["source"]
		delete(hit, "source")
		hit["match_offset"] = at
		b, _ := json.Marshal(hit)
		if len(hits) == 20 || (len(hits) > 0 && total+len(b) > 20000) {
			more = true
			break
		}
		hits = append(hits, hit)
		total += len(b)
	}
	next := ""
	if more {
		next = hits[len(hits)-1]["ref"].(string)
	}
	b, _ := json.Marshal(map[string]any{"matches": hits, "next_after": next, "eof": !more, "note": "Newest decoded source first. Use ref + offset to read directly near the match; after continues to older evidence. Tool outcome is not correctness. Shell commands and original payloads remain accessible by explicit raw event id."})
	return string(b), nil
}
