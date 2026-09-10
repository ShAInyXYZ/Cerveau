package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
	"cerveau/internal/window"
)

func TestLatestPlan(t *testing.T) {
	tmp := t.TempDir()
	eventsPath := filepath.Join(tmp, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.MsgUser, map[string]string{"text": "let's plan"})
	wr.Append(episodic.Plan, map[string]any{
		"title": "Refactor auth",
		"steps": []map[string]any{
			{"title": "extract middleware", "detail": "move auth to its own package", "files": []string{"auth.go"}},
			{"title": "add tests"},
		},
		"autonomy_budget": "low",
	})
	wr.Close()

	plan, evtID, err := LatestPlan(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Title != "Refactor auth" || len(plan.Steps) != 2 || plan.Steps[0].Files[0] != "auth.go" {
		t.Fatalf("plan = %+v", plan)
	}
	if evtID == "" {
		t.Fatal("no plan event id")
	}
}

func TestAutopilotFreshWindow(t *testing.T) {
	tmp := t.TempDir()
	var firstReq []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if firstReq == nil {
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			firstReq = body
		}
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": "step done"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.MsgUser, map[string]string{"text": "SECRET-CHATTER-MARKER old discussion"})
	wr.Append(episodic.MsgAssistant, map[string]any{"text": "old reply"})
	wr.Append(episodic.MsgUser, map[string]string{"text": "TASK-CONSTRAINT-MARKER"})
	os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("fixture"), 0600)
	wr.Append(episodic.Plan, map[string]any{
		"title": "PLAN-MARKER refactor",
		"steps": []map[string]any{{"title": "do the thing", "files": []string{"a.txt"}, "verify": map[string]any{"kind": "contains", "file": "a.txt", "symbol": "fixture"}}},
	})
	wr.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	l.SetWorkspaceFunc(func(string) string { return tmp })
	res, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Reply, "Autopilot report") || !strings.Contains(res.Reply, "1 verified") {
		t.Fatalf("reply = %q", res.Reply)
	}
	if firstReq == nil {
		t.Fatal("no llm request captured")
	}
	req := string(firstReq)
	if !strings.Contains(req, "PLAN-MARKER") {
		t.Fatal("plan payload missing from fresh window")
	}
	if strings.Contains(req, "SECRET-CHATTER-MARKER") {
		t.Fatal("chat history leaked into the autopilot fresh window")
	}

	events, _ := episodic.Replay(eventsPath)
	var cp *episodic.Event
	for i, ev := range events {
		if ev.Type == episodic.Checkpoint {
			cp = &events[i]
		}
	}
	if cp == nil {
		t.Fatal("no checkpoint event")
	}
	var payload struct {
		Status string `json:"status"`
	}
	json.Unmarshal(cp.Payload, &payload)
	if payload.Status != "done" {
		t.Fatalf("checkpoint = %+v", payload)
	}
}

func TestAutopilotHandbackOnFailure(t *testing.T) {
	tmp := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id":   "c1",
						"type": "function",
						"function": map[string]string{
							"name":      "read",
							"arguments": `{"path":"missing.txt"}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	sessDir := filepath.Join(tmp, "sessions")
	eventsPath := filepath.Join(sessDir, "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.Plan, map[string]any{
		"title": "failing plan",
		"steps": []map[string]any{{"title": "will fail"}, {"title": "never runs"}},
	})
	wr.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)

	res, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "plan_blocked" {
		t.Fatalf("stop = %s", res.StopReason)
	}
	if !strings.Contains(res.Reply, "1 blocked") || !strings.Contains(res.Reply, "1 not started") {
		t.Fatalf("reply = %q", res.Reply)
	}
}

// A plan step's window must be COMPRESSED like a chat turn's. runStep used
// to send raw items, so a long step grew until the request exceeded the
// model's context and the run died with "exceeds the available context size".
func TestStepWindowIsCompressed(t *testing.T) {
	mgr := window.NewManager(4000, 500, window.CounterFunc(func(_ context.Context, s string) int {
		return len(s) / 4
	}))
	items := []window.Item{{Msg: llm.Message{Role: "system", Content: "sys"}, Kind: "system"}}
	for i := 0; i < 200; i++ {
		items = append(items, window.Item{
			Msg:  llm.Message{Role: "tool", Content: strings.Repeat("x", 4000)},
			Kind: "tool", EvtID: "evt_" + strconv.Itoa(i),
		})
	}
	raw := 0
	for _, it := range items {
		raw += len(it.Msg.Content) / 4
	}
	if raw <= 4000 {
		t.Fatalf("test setup should overflow the budget, got %d", raw)
	}
	msgs, rep := mgr.Build(context.Background(), items)
	sent := 0
	for _, m := range msgs {
		sent += len(m.Content) / 4
	}
	if sent >= raw {
		t.Fatalf("window did not compress: sent %d of raw %d", sent, raw)
	}
	if rep.Demoted == 0 && rep.Trimmed == 0 {
		t.Fatal("nothing was demoted or trimmed")
	}
}

// A write to a file the committed plan never declared must produce a visible
// note — architecture drift (extra files) was silent before, and the model
// split one-file builds into modules without anyone noticing until runtime.
func TestOutOfPlanNote(t *testing.T) {
	p := &Plan{Title: "game", Steps: []PlanStep{
		{Title: "build", Files: []string{"snake/index.html"}},
	}}
	// declared file: no note
	if n := outOfPlanNote(p, "write", []byte(`{"path":"snake/index.html","content":"x"}`)); n != "" {
		t.Errorf("declared file should be silent, got %q", n)
	}
	// undeclared file: note names both the file and the plan's files
	n := outOfPlanNote(p, "write", []byte(`{"path":"snake/game.js","content":"x"}`))
	if !strings.Contains(n, "snake/game.js") || !strings.Contains(n, "not in the committed plan") {
		t.Errorf("undeclared write should be flagged, got %q", n)
	}
	// non-write tools: never a note
	if n := outOfPlanNote(p, "read", []byte(`{"path":"other.js"}`)); n != "" {
		t.Errorf("read should never be flagged, got %q", n)
	}
	// no plan, or a plan with no declared files: silent
	if n := outOfPlanNote(nil, "write", []byte(`{"path":"a.js"}`)); n != "" {
		t.Errorf("nil plan should be silent, got %q", n)
	}
	if n := outOfPlanNote(&Plan{Title: "x"}, "write", []byte(`{"path":"a.js"}`)); n != "" {
		t.Errorf("plan without files should be silent, got %q", n)
	}
}

// A plan written as a FILE (the model's stubborn habit) must be auto-committed
// as a structured plan event, so the plan card and the Planner RFX see it —
// harness translation instead of prompt pleading.
func TestAutoCommitPlanFile(t *testing.T) {
	dir := t.TempDir()
	wr, err := episodic.Open(dir + "/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	md := "# Chess Build\n## Engine\nWrite `src/engine.ts`\n## Renderer\nBoard and pieces\n"
	args := []byte(`{"path":"docs/plan.md","content":` + strconv.Quote(md) + `}`)

	plan, note := autoCommitPlanFile(wr, "write", args)
	if plan == nil {
		t.Fatal("plan-like write should auto-commit")
	}
	if plan.Title != "Chess Build" || len(plan.Steps) != 2 {
		t.Errorf("parsed plan wrong: %+v", plan)
	}
	if note == "" || !strings.Contains(note, "plan") {
		t.Errorf("the auto-commit must be disclosed: %q", note)
	}

	// and it must be discoverable exactly like a hand-committed plan
	got, _, err := LatestPlan(dir + "/events.jsonl")
	if err != nil || got == nil || got.Title != "Chess Build" {
		t.Fatalf("LatestPlan should find the auto-committed plan: %+v %v", got, err)
	}

	// ordinary writes never auto-commit
	if p, _ := autoCommitPlanFile(wr, "write", []byte(`{"path":"main.go","content":"package main"}`)); p != nil {
		t.Error("code writes must not commit plans")
	}
}

// The whole point of the stepwise design: a step is done when its own check
// says so, not when the model says so.
//
// Here the model reports success on every step, but step 2's check cannot pass
// because the file never gets the symbol it requires. The old loop marked all
// three done (and the report inferred "done" from files existing). The
// supervisor must retry step 2, then hand back — without ever reaching step 3.
func TestAutopilotStopsWhenAStepsOwnCheckFails(t *testing.T) {
	tmp := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"role": "assistant", "content": "step done"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	// step 1 and 3 will pass; step 2 requires a symbol nothing writes
	os.WriteFile(filepath.Join(tmp, "a.js"), []byte("export const a = 1;"), 0o644)
	os.WriteFile(filepath.Join(tmp, "b.js"), []byte("// empty on purpose"), 0o644)

	eventsPath := filepath.Join(tmp, "sessions", "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.Plan, map[string]any{
		"title": "three steps",
		"steps": []map[string]any{
			{"title": "one", "files": []string{"a.js"},
				"verify": map[string]any{"kind": "contains", "file": "a.js", "symbol": "export const a"}},
			{"title": "two", "files": []string{"b.js"},
				"verify": map[string]any{"kind": "contains", "file": "b.js", "symbol": "buildEverything"}},
			{"title": "three", "files": []string{"a.js"},
				"verify": map[string]any{"kind": "contains", "file": "a.js", "symbol": "export const a"}},
		},
	})
	wr.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	l.SetWorkspaceFunc(func(string) string { return tmp })

	res, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}

	// It must NOT claim three done just because the model said so each time.
	if strings.Contains(res.Reply, "3 verified") {
		t.Errorf("a failing check must not read as done:\n%s", res.Reply)
	}
	// And it must stop rather than march on to step 3.
	if !strings.Contains(res.Reply, "1 verified") {
		t.Errorf("step 1 passed its real check, so it should be done:\n%s", res.Reply)
	}

	// The checkpoints are the record: step 2 must appear with its check.
	events, _ := episodic.Replay(eventsPath)
	var sawFailingCheck bool
	for _, e := range events {
		if e.Type != episodic.Checkpoint {
			continue
		}
		var cp struct {
			Step, Status, Check string
		}
		json.Unmarshal(e.Payload, &cp)
		if cp.Step == "two" && cp.Status != "done" && strings.Contains(cp.Check, "buildEverything") {
			sawFailingCheck = true
		}
	}
	if !sawFailingCheck {
		t.Error("no checkpoint recorded step 2 failing its declared check")
	}
}

// A run that says an EARLIER step is incomplete reopens it, then resumes —
// rather than patching around the gap in the later step, which is how a
// workaround for step 1's omission ends up buried in step 3.
func TestAutopilotReopensAnEarlierStepOnRequest(t *testing.T) {
	tmp := t.TempDir()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := "step done"
		// The second step's first run asks for step 1 back. On the revision
		// run the file gains the symbol step 2 needs.
		if calls == 2 {
			json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"id": "rev1", "type": "function", "function": map[string]string{"name": "request_revision", "arguments": `{"target":0,"reason":"declare the shared state object"}`}}}}, "finish_reason": "tool_calls"}}})
			return
		}
		// the revision run adds what step 2 said was missing
		if calls == 3 {
			os.WriteFile(filepath.Join(tmp, "a.js"),
				[]byte("export const state = {};\nexport const shared = {};"), 0o644)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"role": "assistant", "content": body},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	// Step 1 passes its own check on the first run, so the cursor reaches step
	// 2 — which then discovers that step 1 left out something it needs.
	os.WriteFile(filepath.Join(tmp, "a.js"), []byte("export const state = {};"), 0o644)
	os.WriteFile(filepath.Join(tmp, "b.js"), []byte("import { state } from './a.js';"), 0o644)

	eventsPath := filepath.Join(tmp, "sessions", "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.Plan, map[string]any{
		"title": "two steps",
		"steps": []map[string]any{
			{"title": "state", "files": []string{"a.js"},
				"verify": map[string]any{"kind": "contains", "file": "a.js", "symbol": "export const state"}},
			{"title": "consumer", "files": []string{"b.js"},
				"verify": map[string]any{"kind": "contains", "file": "b.js", "symbol": "import { state }"}},
		},
	})
	wr.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	l.SetWorkspaceFunc(func(string) string { return tmp })

	if _, err := l.RunAutopilot(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}

	events, _ := episodic.Replay(eventsPath)
	var revised bool
	for _, e := range events {
		if e.Type != episodic.Checkpoint {
			continue
		}
		var cp struct {
			Step, Decision string
			Rev            int
		}
		json.Unmarshal(e.Payload, &cp)
		if cp.Decision == "revise" || cp.Rev > 0 {
			revised = true
		}
	}
	if !revised {
		t.Error("a run asking for step 1 should have reopened it as a revision")
	}
}
