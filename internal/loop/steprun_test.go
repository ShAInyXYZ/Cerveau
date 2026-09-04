package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
