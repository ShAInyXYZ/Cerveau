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
	"cerveau/internal/plan"
	"cerveau/internal/tools"
)

type sourceChangingCheck struct {
	path    string
	content string
}

func (t sourceChangingCheck) Name() string        { return "bash" }
func (t sourceChangingCheck) Description() string { return "test check with a source side effect" }
func (t sourceChangingCheck) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}}
}
func (t sourceChangingCheck) Execute(context.Context, json.RawMessage) (string, error) {
	if err := os.WriteFile(t.path, []byte(t.content), 0600); err != nil {
		return "", err
	}
	return "check passed", nil
}

func TestPlanAmendmentCheckReceiptRequiresStableDeclaredBytes(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "same_bytes"
		if changed {
			name = "changed_bytes"
		}
		t.Run(name, func(t *testing.T) {
			m := newScriptedModel(textReply("unused"))
			defer m.srv.Close()
			l, journal := gateFixture(t, m)
			workspace := t.TempDir()
			source := filepath.Join(workspace, "world.js")
			if err := os.WriteFile(source, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			content := "original"
			if changed {
				content = "modified"
			}
			l.SetWorkspaceFunc(func(string) string { return workspace })
			reg := tools.NewRegistry(tools.Entry{Tool: sourceChangingCheck{path: source, content: content}, RiskTier: tools.RiskSafe})
			l.SetRegistry(reg)
			check := &plan.Verify{Kind: "command", Command: "fixture-check"}
			w, err := episodic.Open(journal)
			if err != nil {
				t.Fatal(err)
			}
			w.Append(episodic.Plan, &Plan{Title: "receipt", Steps: []PlanStep{{Title: "state", Files: []string{"world.js"}, Verify: check}}})
			w.Close()
			ctx, h, finish, err := l.beginRun(context.Background(), "s1", "autopilot", "")
			if err != nil {
				t.Fatal(err)
			}
			defer finish(nil, nil)
			h.registry = reg
			verdict := l.verifyStep(ctx, h.writer, "s1", 0, check)
			if !verdict.Pass {
				t.Fatalf("metadata binding altered pass predicate: %+v", verdict)
			}
			if changed && verdict.DeclaredSourceVersion != "" {
				t.Fatal("source-changing check gained amendment authority")
			}
			if !changed && verdict.DeclaredSourceVersion == "" {
				t.Fatal("unchanged bytes lost stable content receipt")
			}
		})
	}
}

func TestPlanAmendmentSameRunPromptRefreshAndBudgetRetention(t *testing.T) {
	for _, exhaust := range []bool{false, true} {
		name := "refresh"
		if exhaust {
			name = "spent_generated_budget"
		}
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []string
			var journal, workspace string
			guidance := "Retain state through the unload operation and initialize it through one owner."
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				n := len(bodies)
				bodies = append(bodies, string(body))
				mu.Unlock()
				reply := textReply("repair complete")
				switch n {
				case 0:
					events, err := episodic.Replay(journal)
					if err != nil {
						t.Error(err)
						http.Error(w, "fixture", 500)
						return
					}
					s, id, err := ReducePlan(events)
					if err != nil {
						t.Error(err)
						http.Error(w, "fixture", 500)
						return
					}
					ref := ""
					for _, event := range events {
						var note struct{ Kind string }
						json.Unmarshal(event.Payload, &note)
						if event.Type == episodic.Note && note.Kind == "verify_finished" {
							ref = event.ID
						}
					}
					input := planAmendmentInput{ExpectedRevision: effectivePlanRevision(id, s.Plan), StepID: plan.StepID(s.Plan.Steps[0].ID, 0), ExpectedWorkspaceVersion: planAmendmentWorkspaceVersion(workspace, s.Plan.Steps[0]), Guidance: guidance, Reason: "The current failed invariant contradicts the initialization assumption; preserve every acceptance assertion.", EvidenceEventIDs: []string{ref}}
					raw, _ := json.Marshal(input)
					reply = toolCall(planAdaptationName, string(raw))
				case 1:
					reply = toolCall("read_plan_step", `{"index":0}`)
				case 2:
					reply = toolCall("edit", `{"path":"world.js","old_string":"missing","new_string":"initialized"}`)
				}
				completion := 3
				if exhaust {
					completion = maxTurnTokens * (maxTokenExtensions + 1)
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{reply}, "usage": map[string]int{"prompt_tokens": 5, "completion_tokens": completion}})
			}))
			defer srv.Close()
			l, path := gateFixture(t, &scriptedModel{srv: srv})
			journal = path
			workspace = t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, "world.js"), []byte("const state = 'missing';\n"), 0600); err != nil {
				t.Fatal(err)
			}
			l.SetWorkspaceFunc(func(string) string { return workspace })
			l.SetRegistry(tools.NewRegistry(tools.Entry{Tool: tools.NewRead(workspace), RiskTier: tools.RiskSafe}, tools.Entry{Tool: tools.NewEdit(workspace), RiskTier: tools.RiskSensitive}))
			w, err := episodic.Open(journal)
			if err != nil {
				t.Fatal(err)
			}
			w.Append(episodic.MsgUser, map[string]string{"text": "Preserve every original invariant and acceptance check."})
			w.Append(episodic.Plan, &Plan{Title: "amendment recovery", Steps: []PlanStep{{Title: "state", Files: []string{"world.js"}, Verify: &plan.Verify{Kind: "contains", File: "world.js", Symbol: "initialized"}}}})
			w.Append(episodic.Checkpoint, map[string]any{"index": 0, "status": "blocked", "evidence": "previous failure"})
			w.Close()
			result, err := l.RunStep(context.Background(), "s1", StepRunRequest{Step: -1, Continue: true})
			if err != nil {
				t.Fatal(err)
			}
			events, _ := episodic.Replay(journal)
			effective, _, err := ReducePlan(events)
			if err != nil || len(effective.Plan.Amendments) != 1 {
				for _, event := range events {
					var p struct {
						Name   string
						Output string
					}
					json.Unmarshal(event.Payload, &p)
					if event.Type == episodic.ToolResult && p.Name == planAdaptationName {
						t.Logf("amendment dispatch: %s", p.Output)
					}
				}
				t.Fatalf("amendment did not persist through execution: %+v %v result=%+v", effective, err, result)
			}
			mu.Lock()
			captured := append([]string(nil), bodies...)
			mu.Unlock()
			if exhaust {
				if len(captured) != 1 || result.StopReason != "plan_blocked" || !strings.Contains(result.Reply, "generated-token budget") {
					t.Fatalf("amendment reset spent budget: calls=%d result=%+v", len(captured), result)
				}
			} else {
				if len(captured) != 4 || result.StopReason == "plan_blocked" || !effective.Done() {
					t.Fatalf("same-run continuation failed: calls=%d result=%+v", len(captured), result)
				}
				if !strings.Contains(captured[1], guidance) || !strings.Contains(captured[1], effective.Plan.Revision) || !strings.Contains(captured[2], `implementation_guidance`) || !strings.Contains(captured[2], `initialized`) {
					t.Fatal("effective pinned frame/read_plan_step did not refresh with unchanged check")
				}
			}
			if effective.Steps[0].Attempts != 1 {
				t.Fatalf("amendment restarted supervisor attempt: %+v", effective.Steps[0])
			}
			runs := map[string]bool{}
			baselineChecks := 0
			for _, event := range events {
				var payload struct {
					RunID string `json:"run_id"`
					Kind  string
				}
				json.Unmarshal(event.Payload, &payload)
				if event.Type == episodic.ToolCall || event.Type == episodic.ToolResult {
					runs[payload.RunID] = true
				}
				if payload.Kind == "recovery_source_continuation" {
					t.Fatal("guidance bought source-continuation credit")
				}
				if payload.Kind == "verify_started" {
					baselineChecks++
				}
			}
			if len(runs) != 1 || baselineChecks != 2 {
				t.Fatalf("amendment restarted run/check cycle: runs=%v checks=%d", runs, baselineChecks)
			}
		})
	}
}
