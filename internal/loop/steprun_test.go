package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
)

func stepRunFixture(t *testing.T) (*Loop, string, func()) {
	t.Helper()
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
	os.WriteFile(filepath.Join(tmp, "a.js"), []byte("export const a = 1;"), 0o644)
	os.WriteFile(filepath.Join(tmp, "b.js"), []byte("export const b = 2;"), 0o644)

	eventsPath := filepath.Join(tmp, "sessions", "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.Plan, map[string]any{
		"title": "two",
		"steps": []map[string]any{
			{"title": "one", "files": []string{"a.js"},
				"verify": map[string]any{"kind": "contains", "file": "a.js", "symbol": "export const a"}},
			{"title": "two", "files": []string{"b.js"},
				"verify": map[string]any{"kind": "contains", "file": "b.js", "symbol": "export const b"}},
		},
	})
	wr.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(tools.Entry{Tool: tools.NewRead(tmp), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	l.SetWorkspaceFunc(func(string) string { return tmp })
	return l, tmp, srv.Close
}

// A button that says "run step 2" runs step 2 and stops.
func TestRunStepRunsOnlyThatStep(t *testing.T) {
	l, _, done := stepRunFixture(t)
	defer done()

	if _, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: 1}); err != nil {
		t.Fatal(err)
	}
	st, err := l.PlanStateOf("s1")
	if err != nil {
		t.Fatal(err)
	}
	if st.Steps[1].Status != "passed" {
		t.Errorf("the requested step should have run: %+v", st.Steps[1])
	}
	if st.Steps[0].Status == "passed" {
		t.Errorf("step 1 was not asked for and must not have run: %+v", st.Steps[0])
	}
}

// State survives the request: a panel button, a CLI call and a resumed session
// all read the cursor from the log, so they cannot disagree.
func TestPlanStateRestoresFromCheckpoints(t *testing.T) {
	l, _, done := stepRunFixture(t)
	defer done()

	before, _ := l.PlanStateOf("s1")
	if before.Next != 0 || before.Done {
		t.Fatalf("fresh plan should start at step 1: %+v", before)
	}

	if _, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1}); err != nil {
		t.Fatal(err)
	}
	after, _ := l.PlanStateOf("s1")
	if after.Steps[0].Status != "passed" {
		t.Errorf("step 1 should have passed: %+v", after.Steps[0])
	}
	if after.Next != 1 {
		t.Errorf("cursor should have advanced to step 2, got %d", after.Next)
	}

	// finish it, and the plan reads done
	if _, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1}); err != nil {
		t.Fatal(err)
	}
	end, _ := l.PlanStateOf("s1")
	if !end.Done || end.Next != -1 {
		t.Errorf("plan should be done: %+v", end)
	}
}

// Re-running a passed step is a revision, not a fresh attempt.
func TestRunStepOnAPassedStepIsARevision(t *testing.T) {
	l, _, done := stepRunFixture(t)
	defer done()

	l.RunStep(context.Background(), "s1", StepRunRequest{Step: 0})
	l.RunStep(context.Background(), "s1", StepRunRequest{Step: 0})

	st, _ := l.PlanStateOf("s1")
	if st.Steps[0].Rev < 1 {
		t.Errorf("re-running a passed step should record a revision: %+v", st.Steps[0])
	}
}

func TestRunStepRefusesAStepThatDoesNotExist(t *testing.T) {
	l, _, done := stepRunFixture(t)
	defer done()
	if _, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: 9}); err == nil {
		t.Fatal("want an error naming the plan's size")
	}
}

// The step runner used the GLOBAL registry while the check read the SESSION
// workspace: a step wrote its file, successfully, into the wrong tree and then
// failed its own check for a missing file. Global and session roots differ
// here on purpose, so the two can never be confused again.
func TestStepRunsInTheSessionWorkspaceNotTheGlobalOne(t *testing.T) {
	globalWS := t.TempDir()
	sessionWS := t.TempDir()
	// write once, then report done — a stub that writes on every call never
	// ends the step, and a step that never ends is never checked
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		choice := map[string]any{"message": map[string]any{"role": "assistant", "content": "done"}, "finish_reason": "stop"}
		if calls == 1 {
			choice = map[string]any{
				"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []map[string]any{{
					"id": "w1", "type": "function",
					"function": map[string]any{"name": "write", "arguments": `{"path":"js/config.js","content":"export const CAR = {};"}`},
				}}},
				"finish_reason": "tool_calls",
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{choice},
			"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	eventsPath := filepath.Join(sessionWS, "sessions", "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.Plan, map[string]any{
		"title": "one step",
		"steps": []map[string]any{{"title": "config", "files": []string{"js/config.js"},
			"verify": map[string]any{"kind": "contains", "file": "js/config.js", "symbol": "export const CAR"}}},
	})
	wr.Close()

	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	// the global registry is jailed to globalWS — the bug's destination
	global := tools.NewRegistry(tools.Entry{Tool: tools.NewWrite(globalWS), RiskTier: tools.RiskSafe})
	l := New(llm.NewClient(srv.URL), global, open, func(string) string { return eventsPath }, nil)
	l.SetWorkspaceFunc(func(string) string { return sessionWS })
	// and the per-workspace builder yields one jailed to the session
	l.SetRegistryForWorkspace(func(ws string) *tools.Registry {
		return tools.NewRegistry(tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSafe})
	})

	if _, err := l.RunAutopilot(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sessionWS, "js", "config.js")); err != nil {
		t.Errorf("the step must write into the SESSION workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(globalWS, "js", "config.js")); err == nil {
		t.Error("the step must NOT write into the global workspace")
	}
	st, _ := l.PlanStateOf("s1")
	if st == nil || st.Steps[0].Status != "passed" {
		t.Errorf("with the file in the right place the step's own check must pass: %+v", st)
	}
}

// A step whose run overruns is judged by its CHECK, not its exit. The model
// wrote the file and then kept reading instead of stopping; the file
// satisfied the check, and the step used to be marked failed with the check
// never run.
func TestStepThatOverrunsIsStillJudgedByItsCheck(t *testing.T) {
	ws := t.TempDir()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// call 1 writes the file; every call after that reads, forever
		tc := map[string]any{"name": "read", "arguments": `{"path":"js/config.js"}`}
		if calls == 1 {
			tc = map[string]any{"name": "write", "arguments": `{"path":"js/config.js","content":"export const PHYSICS = {};"}`}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []map[string]any{{
					"id": "c", "type": "function", "function": tc}}},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	eventsPath := filepath.Join(ws, "sessions", "s1", "events.jsonl")
	os.MkdirAll(filepath.Dir(eventsPath), 0o755)
	wr, _ := episodic.Open(eventsPath)
	wr.Append(episodic.Plan, map[string]any{
		"title": "one", "steps": []map[string]any{{"title": "config", "files": []string{"js/config.js"},
			"verify": map[string]any{"kind": "contains", "file": "js/config.js", "symbol": "PHYSICS"}}},
	})
	wr.Close()
	open := func(id string) (*episodic.Writer, error) { return episodic.Open(eventsPath) }
	reg := tools.NewRegistry(
		tools.Entry{Tool: tools.NewWrite(ws), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe},
	)
	l := New(llm.NewClient(srv.URL), reg, open, func(string) string { return eventsPath }, nil)
	l.SetWorkspaceFunc(func(string) string { return ws })

	res, err := l.RunAutopilot(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	st, _ := l.PlanStateOf("s1")
	if st == nil || st.Steps[0].Status != "passed" {
		t.Errorf("the file satisfies the check, so the step is DONE however the run ended: %+v", st)
	}
	if !strings.Contains(res.Reply, "1 done") {
		t.Errorf("report should count it done:\n%s", res.Reply)
	}
}
