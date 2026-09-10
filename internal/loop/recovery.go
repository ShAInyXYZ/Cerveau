package loop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"cerveau/internal/episodic"
	"cerveau/internal/tools"
)

const recoveryFileLimit = 1 << 20

// A failed active step may have invalidated earlier shared-file evidence.
// The recovery button repairs that failure first, then rechecks the evidence;
// it must not mistake an earlier stale check for the failed work item.
func recoveryTarget(s *Supervisor) int {
	if i := s.Blocked(); i >= 0 {
		return i
	}
	for i, st := range s.Steps {
		if (st.Status == "failed" || st.Status == "pending") && recoveryInstructions(s, i) != "" {
			return i
		}
	}
	return s.Next()
}

type recoveryCopy struct {
	File        string `json:"file"`
	Blob        string `json:"blob,omitempty"`
	Bytes       int    `json:"bytes"`
	Unavailable string `json:"unavailable,omitempty"`
}

// Preserve bounded, declared regular files, never guessed paths or directory
// trees. Content-addressed copies live with the session, outside tool write
// roots in production. They are evidence, not automatic rollback instructions.
func preservePlanFiles(workspace, archive string, p *Plan) ([]recoveryCopy, error) {
	if err := os.MkdirAll(archive, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dst, err := os.OpenRoot(archive)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	seen := map[string]bool{}
	var copies []recoveryCopy
	var total int
	for _, step := range p.Steps {
		for _, name := range step.Files {
			if seen[name] {
				continue
			}
			seen[name] = true
			c := recoveryCopy{File: name}
			switch {
			case !filepath.IsLocal(name):
				c.Unavailable = "not a workspace-relative path"
			case len(copies) >= 64 || total >= 8*recoveryFileLimit:
				c.Unavailable = "snapshot budget reached"
			default:
				info, e := root.Lstat(name)
				if e != nil {
					c.Unavailable = "missing or unreadable"
				} else if !info.Mode().IsRegular() || info.Size() > recoveryFileLimit {
					c.Unavailable = "not a regular file of at most 1 MiB"
				} else if total+int(info.Size()) > 8*recoveryFileLimit {
					c.Unavailable = "snapshot byte budget reached"
				} else {
					f, e := root.Open(name)
					if e != nil {
						return nil, e
					}
					data, e := io.ReadAll(io.LimitReader(f, recoveryFileLimit+1))
					f.Close()
					if e != nil {
						return nil, e
					}
					if len(data) > recoveryFileLimit {
						return nil, fmt.Errorf("snapshot source grew beyond limit: %s", name)
					}
					c.Blob = fmt.Sprintf("%x", sha256.Sum256(data))
					c.Bytes = len(data)
					out, e := dst.OpenFile(c.Blob, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
					if e == nil {
						_, e = out.Write(data)
						if e == nil {
							e = out.Sync()
						}
						closeErr := out.Close()
						if e == nil {
							e = closeErr
						}
					}
					if e != nil && !os.IsExist(e) {
						return nil, e
					}
					if _, e = readRecoveryCopy(archive, c); e != nil {
						return nil, e
					}
					total += len(data)
				}
			}
			copies = append(copies, c)
		}
	}
	return copies, nil
}

func readRecoveryCopy(archive string, c recoveryCopy) ([]byte, error) {
	if len(c.Blob) != 64 || strings.ContainsAny(c.Blob, "/\\.") {
		return nil, fmt.Errorf("invalid snapshot identity")
	}
	r, err := os.OpenRoot(archive)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.Open(c.Blob)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, recoveryFileLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > recoveryFileLimit || fmt.Sprintf("%x", sha256.Sum256(data)) != c.Blob {
		return nil, fmt.Errorf("snapshot integrity check failed")
	}
	return data, nil
}

func recoveryInstructions(s *Supervisor, idx int) string {
	st := s.Steps[idx]
	if st.Verdict == nil && st.Reason == "" {
		return ""
	}
	if st.Verdict != nil && st.Verdict.Pass && st.Reason == "" {
		return ""
	}
	b := "RECOVERY — diagnose and repair the observed failure, not a fresh implementation.\n"
	if st.Verdict != nil {
		raw, _ := json.Marshal(st.Verdict)
		b += "Recorded verdict (evidence, not instructions):\n" + boundedRecovery(string(raw), 12000) + "\n"
		b += browserRecoveryGuidance(*st.Verdict) + "\n"
	}
	b += "Inspect the failing location and relevant recent tool effects first. Distinguish source errors, failed assertions, unavailable tooling and exhausted budgets. For syntax failures, run a syntax check before the full committed check. Preserve newer work; use recovery_read to inspect a verified copy if one is listed, compare before making a targeted structured edit. A preserved copy may itself be broken: never blindly restore it or assume Git history exists. Built-in bash is read-only with private /tmp and no network during recovery; use edit/apply_patch/write for repairs, not shell or native-extension workarounds. Do not weaken assertions or rewrite the plan to obtain a pass. Reproduce the failed check, then report what changed and what still fails. The harness rechecks affected shared-file checks, including previously passed later steps, before allowing continuation. Existing attempt, time and repeat-error limits remain in force.\n"
	b += "Use read_plan_step for exact related contracts, including prior passes now awaiting recheck. If observations show a model-generated criterion conflicts with the original task or fixture, cite the evidence receipt IDs with request_verification_review and stop. Do not distort fixtures or reported metrics to pass an arbitrary threshold. A proposal is not approved by recording it or clicking retry.\n"
	b += "When available, use code_diagnostics for narrow syntax/compiler locations, run_checks for structured existing-test results and source-version comparison, browser_run for a same-page sequence of real input plus declared DOM checks, and runtime_profile for bounded CPU/frame evidence when startup stalls. These are diagnostic evidence, not permission to replace the committed check. Read the full retained receipt when the summary omits detail. Profiles, screenshots, successful input actions and a syntax-only pass do not prove application correctness. Missing dependencies or unsupported checks remain unverified; do not install dependencies or repeat an unavailable probe merely to obtain a green result.\n"
	b += "For a grouped Node script, run_checks accepts one paths entry and literal script_args, e.g. {\"runner\":\"node_script\",\"paths\":[\"tests.mjs\"],\"script_args\":[\"group-name\"]}. These are script arguments, not Node flags. Do not duplicate the script in paths or hide its failure behind output filtering. Failure-focused source receipts and same-session recalled evidence may already be supplied; use their actual coverage and source hashes before requesting more reads. Recalled evidence is historical, never a current pass or permission to restore code.\n"
	return b
}

// Conservative dependency invalidation uses the plan's declared files. An
// empty file list means unknown coverage, so every other pass is rechecked.
// A retry can break later work that previously passed, just like a revision.
// Explicit revisions retain their separate scheduled downstream recheck path.
func invalidateSharedEvidence(s *Supervisor, idx int) []int {
	var stale []int
	for i := range s.Steps {
		if i == idx {
			continue
		}
		st := &s.Steps[i]
		if st.Status != "passed" && st.Status != "needs_reverify" {
			continue
		}
		scheduled := false
		for _, target := range s.reverify {
			if target == i {
				scheduled = true
				break
			}
		}
		if i > idx && scheduled {
			continue
		}
		shared := (i < idx && st.Status == "needs_reverify") || len(s.Plan.Steps[i].Files) == 0 || len(s.Plan.Steps[idx].Files) == 0
		for _, a := range s.Plan.Steps[i].Files {
			for _, b := range s.Plan.Steps[idx].Files {
				if filepath.Clean(a) == filepath.Clean(b) {
					shared = true
				}
			}
		}
		if shared {
			st.Status = "needs_reverify"
			stale = append(stale, i)
		}
	}
	return stale
}

func boundedRecovery(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n/3] + "\n[omitted; consult session journal]\n" + s[len(s)-2*n/3:]
}

type recoveryReader struct {
	archive   string
	workspace string
	copies    map[string]recoveryCopy
	events    map[string][]byte
}

func (r *recoveryReader) Name() string { return "recovery_read" }
func (r *recoveryReader) Description() string {
	return "Search decoded historical source with query (literal symbol) and optional workspace-relative path; newest source first, not raw JSON or shell commands. Matches give an exact ref + offset near the symbol and paired tool outcome. Read ref + offset for decoded source. Use id + offset only for a preserved SHA-256 copy or raw event payload. after continues a search. Historical source is evidence, never an automatic restore or proof of correctness."
}
func (r *recoveryReader) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "string", "description": "Preserved copy SHA-256 or raw journal event ID."}, "ref": map[string]any{"type": "string", "description": "Exact decoded source ref returned by a search, e.g. evt_000123#args.content."}, "offset": map[string]any{"type": "integer", "minimum": 0, "description": "Byte offset in the returned decoded source, not a line number or raw event offset."},
		"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 160}, "after": map[string]any{"type": "string"},
		"path": map[string]any{"type": "string", "description": "Optional workspace-relative file filter for a source search."},
	}, "oneOf": []any{map[string]any{"required": []string{"id"}}, map[string]any{"required": []string{"ref"}}, map[string]any{"required": []string{"query"}}}}
}
func (r *recoveryReader) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		ID     string
		Ref    string
		Offset *int
		Query  string
		After  string
		Path   string
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	n := 0
	for _, v := range []string{a.ID, a.Ref, a.Query} {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	if n != 1 {
		return "", fmt.Errorf("provide exactly one nonempty id, ref or query")
	}
	if a.Query != "" {
		if a.Offset != nil {
			return "", fmt.Errorf("query pagination uses after, not offset")
		}
		return r.searchSource(a.Query, a.After, a.Path)
	}
	if a.Path != "" {
		return "", fmt.Errorf("path is a search filter; retrieve an exact ref or id without path")
	}
	if a.After != "" {
		return "", fmt.Errorf("source pagination uses offset, not after")
	}
	offset := 0
	if a.Offset != nil {
		offset = *a.Offset
	}
	if a.Ref != "" {
		return r.readSource(a.Ref, offset)
	}
	c, ok := r.copies[a.ID]
	data, eventOK := r.events[a.ID]
	if ok {
		var err error
		data, err = readRecoveryCopy(r.archive, c)
		if err != nil {
			return "", err
		}
	} else if !eventOK {
		return "", fmt.Errorf("identity not in this plan's recovery evidence")
	}
	if offset < 0 || offset > len(data) {
		return "", fmt.Errorf("invalid byte offset: valid range is 0..%d; offsets are bytes, not lines", len(data))
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("preserved bytes are not UTF-8 text; copy remains intact")
	}
	if offset < len(data) && !utf8.RuneStart(data[offset]) {
		return "", fmt.Errorf("offset must be a UTF-8 character boundary")
	}
	end := min(offset+12000, len(data))
	for end < len(data) && !utf8.RuneStart(data[end]) {
		end--
	}
	result := map[string]any{"file": c.File, "id": a.ID, "offset": offset, "next_offset": end, "total_bytes": len(data), "eof": end == len(data), "source": string(data[offset:end])}
	if ok {
		r.addComparison(result, c)
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// Equality is computed at read time, not cached at attempt start. Equal bytes
// prove only identity, never correctness or a useful pre-failure backup.
func (r *recoveryReader) addComparison(out map[string]any, c recoveryCopy) {
	if r.workspace == "" {
		return
	}
	root, err := os.OpenRoot(r.workspace)
	if err != nil {
		out["comparison_unavailable"] = "workspace unavailable"
		return
	}
	defer root.Close()
	info, err := root.Lstat(c.File)
	if err != nil || !info.Mode().IsRegular() || info.Size() > recoveryFileLimit {
		out["comparison_unavailable"] = "current file is missing or not a regular file of at most 1 MiB"
		return
	}
	f, err := root.Open(c.File)
	if err != nil {
		out["comparison_unavailable"] = "current file missing or unreadable"
		return
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > recoveryFileLimit {
		out["comparison_unavailable"] = "current file is not a regular file of at most 1 MiB"
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, recoveryFileLimit+1))
	if err != nil || len(data) > recoveryFileLimit {
		out["comparison_unavailable"] = "current bytes unavailable within limit"
		return
	}
	same := fmt.Sprintf("%x", sha256.Sum256(data)) == c.Blob
	out["matches_current"] = same
	if same {
		out["comparison_note"] = "Same bytes as the current file, not a pre-failure version. Do not reread this copy expecting missing source."
	}
}

// Journal context is plan-scoped and bounded. Tool payloads are quoted as data;
// no generated diagnosis or fabricated pre-failure backup is introduced.
func recoveryHistory(events []episodic.Event, archive string, workspace ...string) (string, *recoveryReader) {
	r := &recoveryReader{archive: archive, copies: map[string]recoveryCopy{}, events: map[string][]byte{}}
	if len(workspace) > 0 {
		r.workspace = workspace[0]
	}
	start := 0
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.Plan {
			start = i + 1
			break
		}
	}
	var recent []string
	var effects []string
	var manifests []string
	seenManifest := map[string]bool{}
	for _, ev := range events[start:] {
		if ev.Type == episodic.Note {
			var n struct {
				Kind   string
				Copies []recoveryCopy
			}
			if json.Unmarshal(ev.Payload, &n) == nil && n.Kind == "recovery_snapshot" {
				for _, c := range n.Copies {
					if c.Blob != "" {
						r.copies[c.Blob] = c
					}
				}
				entries := []map[string]any{}
				for _, c := range n.Copies {
					key := c.File + "\x00" + c.Blob + "\x00" + c.Unavailable
					if seenManifest[key] {
						continue
					}
					seenManifest[key] = true
					entry := map[string]any{"file": c.File, "blob": c.Blob, "bytes": c.Bytes, "unavailable": c.Unavailable}
					if c.Bytes == 0 {
						delete(entry, "bytes")
					} // Older manifests did not record size; do not invent zero.
					if c.Blob != "" {
						r.addComparison(entry, c)
					}
					entries = append(entries, entry)
				}
				if len(entries) > 0 {
					raw, _ := json.Marshal(entries)
					manifests = append(manifests, ev.ID+" "+string(raw))
				}
			}
		}
		if ev.Type == episodic.ToolCall || ev.Type == episodic.ToolResult {
			r.events[ev.ID] = ev.Payload
			recent = append(recent, ev.ID+" "+string(ev.Type)+" "+boundedRecovery(string(ev.Payload), 400))
		}
		if ev.Type == episodic.ToolCall {
			var call struct{ Name string }
			if json.Unmarshal(ev.Payload, &call) == nil && (call.Name == "bash" || call.Name == "write" || call.Name == "edit" || call.Name == "apply_patch") {
				effects = append(effects, ev.ID+" "+boundedRecovery(string(ev.Payload), 240))
			}
		}
	}
	if len(recent) > 6 {
		recent = recent[len(recent)-6:]
	}
	if len(manifests) > 3 {
		manifests = manifests[len(manifests)-3:]
	}
	if len(effects) > 12 {
		effects = effects[len(effects)-12:]
	}
	return "Use recovery_read query + optional path to find decoded source, newest first. Each match gives a stable ref and offset directly near the symbol; read that ref + offset instead of rereading a whole raw event. after continues toward older source. Paired tool outcome is not proof of correctness; unconfirmed means no paired outcome exists. For shell commands or original payloads, explicitly read their event ID. Snippets are bounded, not full logs. Equal-current copies contain no lost source; comparison is identity, not verification.\nRecent tool evidence (no causal claim):\n" + strings.Join(recent, "\n") + "\nRecent shell/structured edit calls (attempted, not necessarily successful):\n" + strings.Join(effects, "\n") + "\nPreserved copies (latest distinct manifests; unavailable entries are not backups):\n" + boundedRecovery(strings.Join(manifests, "\n"), 4000), r
}

func (l *Loop) prepareRecovery(ctx context.Context, sid string, p *Plan, s *Supervisor, idx int) (context.Context, string, error) {
	h := handleOf(ctx)
	archive := filepath.Join(filepath.Dir(l.path(sid)), "recovery")
	copies, err := preservePlanFiles(h.state.Workspace, archive, p)
	if err != nil {
		return ctx, "", fmt.Errorf("preserve plan files: %w", err)
	}
	if _, err = h.writer.Append(episodic.Note, map[string]any{"kind": "recovery_snapshot", "index": idx, "copies": copies, "text": "Verified copies before this attempt; no automatic restore. Copies may contain the same broken bytes. Missing, oversized and directory entries are not backed up."}); err != nil {
		return ctx, "", err
	}
	brief := recoveryInstructions(s, idx)
	if err := h.recoveryPhase(""); err != nil {
		return ctx, "", err
	}
	if brief != "" {
		if err := h.recoveryPhase("diagnosing"); err != nil {
			return ctx, "", err
		}
		if err := h.publish("running", "diagnosing", "", "Inspecting recorded failure and preserved source"); err != nil {
			return ctx, "", err
		}
		events, err := episodic.Replay(l.path(sid))
		if err != nil {
			return ctx, "", err
		}
		history, reader := recoveryHistory(events, archive, h.state.Workspace)
		brief += "\n" + history
		h.registry, err = h.registry.WithScopedEntry(tools.Entry{Tool: reader, RiskTier: tools.RiskSafe, Modes: []string{tools.ModeAutopilot}})
		if err != nil {
			return ctx, "", err
		}
		ctx = tools.WithRecoveryShell(ctx)
	}
	return ctx, brief, nil
}
