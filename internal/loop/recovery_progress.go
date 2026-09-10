package loop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
)

// Recovery memory contains bounded observations, never a model-generated claim
// that a repair passed. The original tool result remains the authority. Notes
// are fsynced through the normal journal owner and survive a host restart.
type recoveryObservation struct {
	Kind      string `json:"kind"`
	PlanID    string `json:"plan_event_id"`
	Index     int    `json:"index"`
	CheckSHA  string `json:"check_sha256"`
	EventID   string `json:"evidence_event_id"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	OutputSHA string `json:"output_sha256"`
	OK        bool   `json:"ok"`
	File      string `json:"file,omitempty"`
	SourceSHA string `json:"source_sha256,omitempty"`
	Coverage  string `json:"observed_coverage,omitempty"`
	Category  string `json:"category"`
	Excerpt   string `json:"excerpt"`
}

type recoveryProgress struct {
	workspace, planID, checkSHA string
	index                       int
	entries                     []recoveryObservation
	newFacts, repairCalls       int
	lastError                   string
	coverage                    *recoveryCoverage
}

func recoverySHA(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func restoreRecoveryProgress(events []episodic.Event, workspace, planID string, idx int, step PlanStep) *recoveryProgress {
	raw, _ := json.Marshal(step)
	p := &recoveryProgress{workspace: workspace, planID: planID, index: idx, checkSHA: recoverySHA(raw), coverage: newRecoveryCoverage(workspace)}
	start := 0
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.Plan {
			if events[i].ID != planID {
				return p
			}
			start = i + 1
			break
		}
	}
	// Validate the durable reference rather than trusting a detached excerpt.
	type resultWithCall struct {
		event episodic.Event
		args  string
	}
	results := map[string]resultWithCall{}
	pending := map[string]string{}
	for _, ev := range events[start:] {
		if ev.Type == episodic.ToolCall || ev.Type == episodic.ToolResult {
			var call struct {
				ID, Name string
				RunID    string `json:"run_id"`
				Args     json.RawMessage
				RawArgs  string `json:"raw_args"`
			}
			if json.Unmarshal(ev.Payload, &call) != nil || call.ID == "" {
				continue
			}
			key := call.RunID + "\x00" + call.Name + "\x00" + call.ID
			if ev.Type == episodic.ToolCall {
				args := string(call.Args)
				if args == "" {
					args = call.RawArgs
				}
				pending[key] = args // synchronous dispatch: newer call supersedes an interrupted orphan
				continue
			}
			if args, ok := pending[key]; ok {
				results[ev.ID] = resultWithCall{ev, args}
				delete(pending, key)
			}
			continue
		}
		if ev.Type != episodic.Note {
			continue
		}
		var o recoveryObservation
		if json.Unmarshal(ev.Payload, &o) != nil || o.Kind != "recovery_observation" || o.PlanID != planID || o.Index != idx || o.CheckSHA != p.checkSHA {
			continue
		}
		result, ok := results[o.EventID]
		if !ok {
			continue
		}
		var payload struct {
			Name, Output string
			OK           bool
		}
		if json.Unmarshal(result.event.Payload, &payload) != nil || payload.Name != o.Tool || payload.OK != o.OK || recoverySHA([]byte(payload.Output)) != o.OutputSHA {
			continue
		}
		// Rebuild bounded fields from the recorded result, not arbitrary note prose.
		fresh := p.observation(o.Tool, result.args, o.EventID, payload.Output, payload.OK)
		if fresh.Arguments != o.Arguments {
			continue
		}
		if fresh.Tool == "read" && fresh.OK {
			p.coverage.observeRead(payload.Output, true)
		}
		p.retain(fresh)
	}
	p.newFacts = 0
	return p
}

func recoveryExcerpt(s string, cap int) string {
	if len(s) <= cap {
		return s
	}
	const notice = "\n[excerpt omitted; retrieve the evidence event/ref for exact source]\n"
	head, tail := (cap-len(notice))/2, (cap-len(notice))/2
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	start := len(s) - tail
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[:head] + notice + s[start:]
}

func (p *recoveryProgress) observation(tool, args, id, out string, ok bool) recoveryObservation {
	// Never store a generated replacement twice; navigation arguments are the
	// only arguments useful as a compact replay reference.
	var object map[string]json.RawMessage
	_ = json.Unmarshal([]byte(args), &object)
	compact := map[string]json.RawMessage{}
	for _, key := range []string{"path", "from_line", "to_line", "offset", "id", "ref", "query", "after", "expected_sha256"} {
		if value, exists := object[key]; exists {
			compact[key] = value
		}
	}
	encoded, _ := json.Marshal(compact)
	o := recoveryObservation{Kind: "recovery_observation", PlanID: p.planID, Index: p.index, CheckSHA: p.checkSHA, EventID: id, Tool: tool, Arguments: recoveryExcerpt(string(encoded), 700), OutputSHA: recoverySHA([]byte(out)), OK: ok, Category: "tool observation", Excerpt: recoveryExcerpt(out, 4000)}
	if tool == "read" && ok {
		o.Category = "current-source read"
		if line, _, found := strings.Cut(out, "\n"); found && strings.HasPrefix(line, "[read ") && strings.HasSuffix(line, "]") {
			var meta struct {
				Path, SHA256 string
				FromLine     int `json:"from_line"`
				ToLine       int `json:"to_line"`
			}
			if json.Unmarshal([]byte(line[6:len(line)-1]), &meta) == nil {
				o.File, o.SourceSHA = meta.Path, meta.SHA256
				o.Coverage = recoveryExcerpt(line, 1200)
			}
		}
	}
	if tool == "recovery_read" {
		o.Category = "historical evidence (not current source)"
		if len(compact["query"]) > 0 {
			o.Category = "source search"
		}
	}
	if tool == "edit" || tool == "write" || tool == "apply_patch" {
		o.Category = "repair tool outcome (not verification)"
	}
	return o
}

func (p *recoveryProgress) retain(o recoveryObservation) bool {
	fresh := true
	for i, old := range p.entries {
		if old.Tool == o.Tool && old.Arguments == o.Arguments && old.OutputSHA == o.OutputSHA {
			p.entries = append(p.entries[:i], p.entries[i+1:]...)
			fresh = false
			break
		}
	}
	p.entries = append(p.entries, o)
	if len(p.entries) > 24 {
		p.entries = p.entries[len(p.entries)-24:]
	}
	return fresh
}

func (p *recoveryProgress) observe(w *episodic.Writer, tc llm.ToolCall, eventID, out string, callErr error) error {
	if eventID == "" {
		return nil
	}
	o := p.observation(tc.Function.Name, tc.Function.Arguments, eventID, out, callErr == nil)
	if _, err := w.Append(episodic.Note, o); err != nil {
		return err
	}
	if p.retain(o) {
		p.newFacts++
	}
	if o.Tool == "read" && o.OK {
		p.coverage.observeRead(out, false)
	}
	if callErr != nil {
		p.lastError = recoveryExcerpt(out, 600)
	}
	if tc.Function.Name == "edit" || tc.Function.Name == "write" || tc.Function.Name == "apply_patch" {
		p.repairCalls++
	}
	return nil
}

func recoveryCurrentSHA(workspace, path string) string {
	if !filepath.IsLocal(path) {
		return ""
	}
	r, err := os.OpenRoot(workspace)
	if err != nil {
		return ""
	}
	defer r.Close()
	info, err := r.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > recoveryFileLimit {
		return ""
	}
	f, err := r.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > recoveryFileLimit {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, recoveryFileLimit+1))
	if err != nil || len(data) > recoveryFileLimit {
		return ""
	}
	return recoverySHA(data)
}

func (p *recoveryProgress) brief() string {
	if len(p.entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("RECOVERY MEMORY — journal-derived observations, not verification. Reuse these facts instead of restarting investigation. Excerpts are shorter than the recorded coverage; retrieve the cited event/ref or exact missing range when needed. Historical source is evidence only; never restore it blindly. A matching file hash proves identity, not correctness.\n")
	current := map[string]string{}
	shown := map[int]bool{}
	// Prioritize acquired source over repetitive shell/search chatter, while
	// preserving recency within each category and a hard serialized byte cap.
	for _, category := range []string{"historical evidence (not current source)", "current-source read", "repair tool outcome (not verification)", "source search", "tool observation"} {
		for i := len(p.entries) - 1; i >= 0; i-- {
			o := p.entries[i]
			if o.Category != category {
				continue
			}
			state := "recorded observation; not verification"
			if o.Category == "current-source read" {
				state = "unversioned read; use a fresh targeted read before editing"
				if o.SourceSHA != "" {
					hash, seen := current[o.File]
					if !seen {
						hash = recoveryCurrentSHA(p.workspace, o.File)
						current[o.File] = hash
					}
					switch {
					case hash == "":
						state = "source version unavailable; recheck before editing"
					case hash == o.SourceSHA:
						state = "matches current source"
					default:
						state = "stale source; file changed, re-read the affected region"
					}
				}
			}
			cap := 1400
			if category == "historical evidence (not current source)" {
				cap = 3000
			}
			row := map[string]any{"event_id": o.EventID, "tool": o.Tool, "arguments": o.Arguments, "category": o.Category, "ok": o.OK, "state": state, "excerpt": recoveryExcerpt(o.Excerpt, cap)}
			if o.Coverage != "" {
				row["observed_coverage"] = o.Coverage
			}
			raw, _ := json.Marshal(row)
			if b.Len()+len(raw) > 15500 {
				continue
			}
			b.Write(raw)
			b.WriteByte('\n')
			shown[i] = true
		}
	}
	if len(shown) < len(p.entries) {
		fmt.Fprintf(&b, "%d additional observations retained in the journal; omitted from this bounded brief.\n", len(p.entries)-len(shown))
	}
	b.WriteString("Next: resolve the specific remaining uncertainty, make a small guarded repair if supported, then run a syntax/focused check. The unchanged committed check and affected shared-file rechecks decide whether the plan may continue.\n")
	return b.String()
}

func (p *recoveryProgress) stopDetail(detail string) string {
	return fmt.Sprintf("%s. Recovery evidence retained: %d recent distinct observations (bounded cache), %d newly indexed observations and %d structured repair calls this attempt (calls are not verified fixes). Resume will reuse the evidence; the latest committed-check result below remains authoritative.", detail, len(p.entries), p.newFacts, p.repairCalls)
}
