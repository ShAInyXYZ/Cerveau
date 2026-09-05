package loop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
)

// A scripted model: reply i is served for call i, and the tools OFFERED on each
// call are recorded so the test can see what the gate allowed.
type scriptedModel struct {
	mu      sync.Mutex
	replies []map[string]any
	offered [][]string
	forced  []string // tool_choice function name per call, "" when not forced
	bodies  []string // raw request body per call
	srv     *httptest.Server
}

func newScriptedModel(replies ...map[string]any) *scriptedModel {
	m := &scriptedModel{replies: replies}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Tools []struct {
				Function struct{ Name string } `json:"function"`
			} `json:"tools"`
			ToolChoice *struct {
				Function struct{ Name string } `json:"function"`
			} `json:"tool_choice"`
		}
		json.Unmarshal(body, &req)
		forced := ""
		if req.ToolChoice != nil {
			forced = req.ToolChoice.Function.Name
		}
		var names []string
		for _, t := range req.Tools {
			names = append(names, t.Function.Name)
		}
		m.mu.Lock()
		i := len(m.offered)
		m.offered = append(m.offered, names)
		m.forced = append(m.forced, forced)
		m.bodies = append(m.bodies, string(body))
		reply := m.replies[len(m.replies)-1]
		if i < len(m.replies) {
			reply = m.replies[i]
		}
		m.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{reply},
			"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	return m
}

func toolCall(name, args string) map[string]any {
	return map[string]any{
		"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []map[string]any{{
			"id": "c-" + name, "type": "function",
			"function": map[string]any{"name": name, "arguments": args},
		}}},
		"finish_reason": "tool_calls",
	}
}

func textReply(s string) map[string]any {
	return map[string]any{"message": map[string]any{"role": "assistant", "content": s}, "finish_reason": "stop"}
}

func gateFixture(t *testing.T, m *scriptedModel) (*Loop, string) {
	t.Helper()
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "index.html"), []byte("<html>435 lines of monolith</html>"), 0o644)
	eventsPath := filepath.Join(tmp, "sessions", "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	sctx := &tools.SessionContext{SessionID: "s1"}
	// Mirror cmd/crv/main.go: commit_plan is mode-fenced. A permissive
	// registry here hid the real bug — fenced to discussion only, the live
	// gate offered an empty tool list on every autopilot planning call.
	reg := tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewCommitPlan(open, sctx), RiskTier: tools.RiskSafe,
			Modes: []string{tools.ModeDiscussion, tools.ModeAutopilot}},
	)
	l := New(llm.NewClient(m.srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	l.SetWorkspaceFunc(func(string) string { return tmp })
	return l, eventsPath
}

// "Improve our car game" cannot be planned without reading the car game. The
// gate used to offer commit_plan alone, so the model either planned blind or
// froze. Now it may read, and the gate stays open until a plan lands.
func TestPlanGateLetsTheModelReadThenCommit(t *testing.T) {
	m := newScriptedModel(
		toolCall("read", `{"path":"index.html"}`),
		toolCall("commit_plan", `{"title":"Restructure","steps":[{"title":"split","files":["src/Game.js"],"verify":{"kind":"contains","file":"src/Game.js","symbol":"class Game"}}]}`),
		textReply("done"),
	)
	defer m.srv.Close()
	l, eventsPath := gateFixture(t, m)

	if _, err := l.Run(context.Background(), "s1", "improve our car game with proper structure", "autopilot"); err != nil {
		t.Fatal(err)
	}

	// call 1 must have been allowed to read
	if len(m.offered) < 2 || !hasTool(m.offered[0], "read") || !hasTool(m.offered[0], "commit_plan") {
		t.Fatalf("planning call should offer read AND commit_plan, offered %v", m.offered)
	}
	// and the read must NOT have closed the gate: call 2 was still a planning call
	events, _ := episodic.Replay(eventsPath)
	var sawRead, sawCommit, sawPlan bool
	for _, e := range events {
		if e.Type == episodic.Plan {
			sawPlan = true
		}
		if e.Type != episodic.Note {
			continue
		}
		var n struct{ Kind, Text string }
		json.Unmarshal(e.Payload, &n)
		if n.Kind == "plan_first" && strings.Contains(n.Text, "read before planning") {
			sawRead = true
		}
		if n.Kind == "plan_first" && strings.Contains(n.Text, "plan committed") {
			sawCommit = true
		}
	}
	if !sawRead {
		t.Error("a read during planning should be noted and keep the gate open")
	}
	if !sawPlan || !sawCommit {
		t.Errorf("the plan should have landed on the call AFTER the read: plan=%v committed=%v", sawPlan, sawCommit)
	}
	// And the turn must have HANDED OFF: steps run under the supervisor, which
	// writes a checkpoint per run. Zero checkpoints means the old behaviour —
	// one long turn with the plan pasted in as guidance.
	var checkpoints int
	for _, e := range events {
		if e.Type == episodic.Checkpoint {
			checkpoints++
		}
	}
	if checkpoints == 0 {
		t.Error("a committed plan must be executed step by step (no checkpoint was written)")
	}
}

// Reading is bounded. After maxPlanReads calls with no plan the model gets
// commit_plan alone, so planning cannot become a tour of the tree.
func TestPlanGateBoundsReading(t *testing.T) {
	replies := []map[string]any{}
	for i := 0; i < maxPlanReads; i++ {
		replies = append(replies, toolCall("read", `{"path":"index.html"}`))
	}
	replies = append(replies, textReply("I have looked at everything"))
	m := newScriptedModel(replies...)
	defer m.srv.Close()
	l, _ := gateFixture(t, m)

	l.Run(context.Background(), "s1", "improve our car game", "autopilot")

	if len(m.offered) <= maxPlanReads {
		t.Fatalf("expected a call after %d reads, got %d calls", maxPlanReads, len(m.offered))
	}
	for i := 0; i < maxPlanReads; i++ {
		if !hasTool(m.offered[i], "read") {
			t.Errorf("call %d should still offer read, offered %v", i+1, m.offered[i])
		}
	}
	last := m.offered[maxPlanReads]
	if len(last) != 1 || last[0] != "commit_plan" {
		t.Errorf("after %d reads the gate must offer commit_plan ONLY, offered %v", maxPlanReads, last)
	}
	// Narrowing the offer was toothless on its own: the model kept calling
	// read and glob from memory. The call must be FORCED.
	if m.forced[maxPlanReads] != "commit_plan" {
		t.Errorf("after %d reads the call must force tool_choice=commit_plan, got %q", maxPlanReads, m.forced[maxPlanReads])
	}
	for i := 0; i < maxPlanReads; i++ {
		if m.forced[i] != "" {
			t.Errorf("call %d is a reading call and must not be forced, got %q", i+1, m.forced[i])
		}
	}
}

// file_map and glob are orientation, not reading: they do not spend the read
// budget. On the car restructure they ate two of three slots and the model was
// refused the second read of a 435-line file.
func TestPlanGateOrientationIsFree(t *testing.T) {
	replies := []map[string]any{
		toolCall("file_map", `{}`),
		toolCall("glob", `{"pattern":"**/*"}`),
	}
	for i := 0; i < maxPlanReads; i++ {
		replies = append(replies, toolCall("read", `{"path":"index.html"}`))
	}
	replies = append(replies, textReply("looked at everything"))
	m := newScriptedModel(replies...)
	defer m.srv.Close()
	l, _ := gateFixture(t, m)

	l.Run(context.Background(), "s1", "improve our car game", "autopilot")

	want := 2 + maxPlanReads // orientation + full read budget, all still offered read
	if len(m.offered) <= want {
		t.Fatalf("expected a call after %d planning calls, got %d", want, len(m.offered))
	}
	for i := 0; i < want; i++ {
		if !hasTool(m.offered[i], "read") {
			t.Errorf("call %d should still offer read (orientation must not spend the budget), offered %v", i+1, m.offered[i])
		}
	}
	if m.forced[want] != "commit_plan" {
		t.Errorf("after orientation + %d reads the call must be forced, got %q", maxPlanReads, m.forced[want])
	}
}

// A commit_plan that Validate refuses is not a read: it must not spend the
// read budget, and the next call is forced so the model fixes the check
// rather than wandering off.
func TestPlanGateForcesRetryAfterARefusedCommit(t *testing.T) {
	m := newScriptedModel(
		// a costume: expr "true" is refused at commit time
		toolCall("commit_plan", `{"title":"P","steps":[{"title":"a","files":["index.html"],"verify":{"kind":"eval","expr":"true","path":"index.html"}}]}`),
		// the retry carries a real check
		toolCall("commit_plan", `{"title":"P","steps":[{"title":"a","files":["index.html"],"verify":{"kind":"contains","file":"index.html","symbol":"monolith"}}]}`),
		textReply("done"),
	)
	defer m.srv.Close()
	l, eventsPath := gateFixture(t, m)

	if _, err := l.Run(context.Background(), "s1", "improve our car game", "autopilot"); err != nil {
		t.Fatal(err)
	}
	if len(m.forced) < 2 || m.forced[1] != "commit_plan" {
		t.Errorf("the call after a refused commit must be forced, got %v", m.forced)
	}
	events, _ := episodic.Replay(eventsPath)
	var refused, committed bool
	for _, e := range events {
		if e.Type == episodic.Plan {
			committed = true
		}
		if e.Type == episodic.Note {
			var n struct{ Kind, Text string }
			json.Unmarshal(e.Payload, &n)
			if n.Kind == "plan_first" && strings.Contains(n.Text, "refused (1/") {
				refused = true
			}
		}
	}
	if !refused {
		t.Error("the refused commit should be noted, not counted as a read")
	}
	if !committed {
		t.Error("the corrected commit should have landed")
	}
}

func hasTool(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// commit_plan fenced away from autopilot is a registration bug. The gate must
// say so and run unplanned — never force tool_choice on an empty offer, which
// vLLM rejects with a 400 and the turn dies on ("When using tool_choice, tools
// must be set" — the improve-forced run, 2026-09-04).
func TestPlanGateRefusesToForceAnEmptyOffer(t *testing.T) {
	m := newScriptedModel(textReply("done"))
	defer m.srv.Close()
	tmp := t.TempDir()
	eventsPath := filepath.Join(tmp, "sessions", "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe},
		// the bug as it shipped: discussion only
		tools.Entry{Tool: tools.NewCommitPlan(open, &tools.SessionContext{SessionID: "s1"}),
			RiskTier: tools.RiskSafe, Modes: []string{tools.ModeDiscussion}},
	)
	l := New(llm.NewClient(m.srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	l.SetWorkspaceFunc(func(string) string { return tmp })

	if _, err := l.Run(context.Background(), "s1", "improve our car game", "autopilot"); err != nil {
		t.Fatalf("the turn must survive a misregistered commit_plan: %v", err)
	}
	for i, f := range m.forced {
		if f != "" {
			t.Errorf("call %d forced %q with commit_plan not on offer — that is the 400", i+1, f)
		}
	}
	events, _ := episodic.Replay(eventsPath)
	var loud bool
	for _, e := range events {
		if e.Type != episodic.Note {
			continue
		}
		var n struct{ Kind, Text string }
		json.Unmarshal(e.Payload, &n)
		if n.Kind == "plan_first" && strings.Contains(n.Text, "not available in autopilot") {
			loud = true
		}
	}
	if !loud {
		t.Error("a missing commit_plan must be reported as a registration bug, not blamed on the model")
	}
}

// The shared SessionContext is overwritten by every chat request. While one
// session was planning, "restart that server" typed in another set it to that
// other session, and the planning session's commit_plan wrote its plan there —
// then read its own log, found nothing, and called the commit refused. The
// session id must come from the run's context, not the global.
func TestCommitPlanLandsInTheRunningSessionNotTheSharedOne(t *testing.T) {
	m := newScriptedModel(
		toolCall("commit_plan", `{"title":"P","steps":[{"title":"a","files":["index.html"],"verify":{"kind":"contains","file":"index.html","symbol":"monolith"}}]}`),
		textReply("done"),
	)
	defer m.srv.Close()
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "index.html"), []byte("monolith"), 0o644)
	mine := filepath.Join(tmp, "sessions", "s1", "events.jsonl")
	other := filepath.Join(tmp, "sessions", "OTHER", "events.jsonl")
	os.MkdirAll(filepath.Dir(mine), 0o755)
	os.MkdirAll(filepath.Dir(other), 0o755)
	open := func(id string) (*episodic.Writer, error) {
		return episodic.Open(filepath.Join(tmp, "sessions", id, "events.jsonl"))
	}
	// the bug as it happened: the shared context points at SOMEONE ELSE
	sctx := &tools.SessionContext{SessionID: "OTHER"}
	reg := tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewCommitPlan(open, sctx), RiskTier: tools.RiskSafe,
			Modes: []string{tools.ModeDiscussion, tools.ModeAutopilot}},
	)
	l := New(llm.NewClient(m.srv.URL), reg, open, func(id string) string {
		return filepath.Join(tmp, "sessions", id, "events.jsonl")
	}, nil)
	l.SetWorkspaceFunc(func(string) string { return tmp })

	if _, err := l.Run(context.Background(), "s1", "improve our car game", "autopilot"); err != nil {
		t.Fatal(err)
	}
	if p, _, err := LatestPlan(mine); err != nil || p == nil {
		t.Errorf("the plan must land in the RUNNING session: %v", err)
	}
	if p, _, _ := LatestPlan(other); p != nil {
		t.Error("the plan must NOT land in the session the shared context happened to name")
	}
}

// A step starts with a fresh window. The reads the plan was made from must
// ride into it, or the model re-reads the whole source under the step's own
// budget and never reaches a write.
func TestStepRequestCarriesThePlanningReads(t *testing.T) {
	m := newScriptedModel(
		toolCall("read", `{"path":"index.html"}`),
		toolCall("commit_plan", `{"title":"R","steps":[{"title":"split","files":["index.html"],"verify":{"kind":"contains","file":"index.html","symbol":"monolith"}}]}`),
		textReply("done"),
	)
	defer m.srv.Close()
	l, _ := gateFixture(t, m)
	if _, err := l.Run(context.Background(), "s1", "improve our car game", "autopilot"); err != nil {
		t.Fatal(err)
	}
	if len(m.bodies) < 3 {
		t.Fatalf("expected a step call after the plan, got %d calls", len(m.bodies))
	}
	// call 3 is the first STEP run; the fixture's index.html says "435 lines of monolith"
	if !strings.Contains(m.bodies[2], "435 lines of monolith") {
		t.Error("the step's request must carry the source read during planning")
	}
	if !strings.Contains(m.bodies[2], "do not re-read it") {
		t.Error("the step must be told the source is already in hand")
	}
}
