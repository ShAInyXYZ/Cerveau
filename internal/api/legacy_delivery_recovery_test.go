package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/loop"
	"cerveau/internal/plan"
	"cerveau/internal/tools"
)

// No subprocesses or real model are involved: even the declared command checks
// are deterministic observations of the isolated fixture source.
type legacyDeliveryRecoveryTool struct {
	name string
	run  func(json.RawMessage) (string, error)
}

func (t legacyDeliveryRecoveryTool) Name() string        { return t.name }
func (t legacyDeliveryRecoveryTool) Description() string { return "Isolated recovery test tool" }
func (t legacyDeliveryRecoveryTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"command": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"},
	}}
}
func (t legacyDeliveryRecoveryTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	return t.run(args)
}

func legacyDeliveryAppend(t *testing.T, wr *episodic.Writer, kind episodic.EventType, payload any) episodic.Event {
	t.Helper()
	ev, err := wr.Append(kind, payload)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func legacyDeliveryWait(t *testing.T, f *runAPIFixture) *loop.RunState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := f.l.RunStateOf(f.sid); st != nil {
			switch st.Status {
			case "completed", "failed", "cancelled", "suspended", "interrupted":
				return st
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Recover command did not finish: %+v", f.l.RunStateOf(f.sid))
	return nil
}

func TestHTTPRecoverLegacyDeliveryRepairsFailedPendingStepAndContinues(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	f := newRunAPIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, "read failed", 500)
			return
		}
		mu.Lock()
		prompts = append(prompts, string(body))
		call := len(prompts)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"repair-lighting","type":"function","function":{"name":"edit","arguments":"{\"path\":\"world.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"The targeted behavior is ready for its unchanged check."},"finish_reason":"stop"}]}`)
		}
	})
	f.l.SetThinking("off", "low")
	meta, err := f.a.sess.Get(f.sid)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(meta.Workspace, "world.txt")
	if err := os.WriteFile(source, []byte("foundation retained\nlighting broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var checks []string
	reg := tools.NewRegistry(
		tools.Entry{Tool: tools.NewRead(meta.Workspace), RiskTier: tools.RiskSafe},
		tools.Entry{Tool: legacyDeliveryRecoveryTool{name: "bash", run: func(raw json.RawMessage) (string, error) {
			var args struct{ Command string }
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			data, err := os.ReadFile(source)
			if err != nil {
				return "", err
			}
			mu.Lock()
			checks = append(checks, args.Command)
			mu.Unlock()
			switch args.Command {
			case "check-foundation":
				if strings.Contains(string(data), "foundation retained") {
					return "FOUNDATION_OK: original foundation retained", nil
				}
			case "check-lighting", "check-integration":
				if strings.Contains(string(data), "foundation retained\nlighting repaired") {
					return "LIGHTING_OK: foundation and lighting verified", nil
				}
			default:
				return "", fmt.Errorf("unexpected fixture command %q", args.Command)
			}
			return "TypeError: Cannot read properties of undefined (reading 'sky')\n    at updateLighting (world.txt:2:1)", fmt.Errorf("fixture check failed")
		}}, RiskTier: tools.RiskSafe},
		tools.Entry{Tool: legacyDeliveryRecoveryTool{name: "edit", run: func(raw json.RawMessage) (string, error) {
			var args struct{ Path string }
			if err := json.Unmarshal(raw, &args); err != nil || args.Path != "world.txt" {
				return "", fmt.Errorf("unexpected fixture edit")
			}
			data, err := os.ReadFile(source)
			if err != nil {
				return "", err
			}
			if !strings.Contains(string(data), "lighting broken") {
				return "", fmt.Errorf("fixture repair already applied")
			}
			if err := os.WriteFile(source, []byte(strings.Replace(string(data), "lighting broken", "lighting repaired", 1)), 0600); err != nil {
				return "", err
			}
			return "Repaired the lighting region; foundation unchanged.", nil
		}}, RiskTier: tools.RiskSensitive},
	)
	f.l.SetRegistry(reg)
	wr, err := f.a.Writer(f.sid)
	if err != nil {
		t.Fatal(err)
	}
	const request = "Build the fixture.\n## DELIVERY ORDER\n1. Preserve the foundation.\n2. Repair lighting.\n3. Verify integration."
	legacyDeliveryAppend(t, wr, episodic.MsgUser, map[string]any{"text": request})
	rawSteps := []map[string]any{}
	for i, title := range []string{"Foundation", "Lighting", "Integration"} {
		rawSteps = append(rawSteps, map[string]any{"title": title, "detail": "Preserve existing behavior while completing " + title, "files": []string{"world.txt"}, "risk": "low", "verify": &plan.Verify{Kind: "command", Command: []string{"check-foundation", "check-lighting", "check-integration"}[i]}})
	}
	// Deliberately raw old schema: no IDs, milestone mappings or delivery field.
	committed := legacyDeliveryAppend(t, wr, episodic.Plan, map[string]any{"title": "Legacy MineKraft-shaped fixture", "steps": rawSteps})
	p, id, err := loop.LatestPlan(f.a.sess.EventsPath(f.sid))
	if err != nil || id != committed.ID {
		t.Fatalf("committed plan: id=%s err=%v", id, err)
	}
	history := wr.Scoped(map[string]any{"session_id": f.sid, "run_id": "historical-harness-run"})
	verdicts := make([]loop.Verdict, 2)
	for i := range verdicts {
		v := p.Steps[i].Verify
		legacyDeliveryAppend(t, history, episodic.Note, map[string]any{"kind": "verify_started", "index": i, "verify": v})
		callID := fmt.Sprintf("historical-verify-%d", i)
		legacyDeliveryAppend(t, history, episodic.ToolCall, map[string]any{"id": callID, "name": "bash", "args": map[string]string{"command": v.Command}})
		verdicts[i] = loop.RunVerify(context.Background(), reg, meta.Workspace, v)
		evidence := legacyDeliveryAppend(t, history, episodic.ToolResult, map[string]any{"id": callID, "name": "bash", "ok": verdicts[i].Pass, "output": verdicts[i].Evidence})
		verdicts[i].EvidenceEventID = evidence.ID
		legacyDeliveryAppend(t, history, episodic.Note, map[string]any{"kind": "verify_finished", "index": i, "verdict": verdicts[i]})
	}
	if !verdicts[0].Pass || verdicts[1].Pass {
		t.Fatal("historical fixture must contain a genuine pass followed by failure")
	}
	sup := loop.NewSupervisor(p)
	sup.Steps[0].Status, sup.Steps[0].Attempts, sup.Steps[0].Verdict = "needs_reverify", 1, &verdicts[0]
	// A prior failed Recover click already changed blocked to pending and reset
	// attempts. The retained false verdict must still select this failed step.
	sup.Steps[1].Status, sup.Steps[1].Attempts, sup.Steps[1].Verdict = "pending", 0, &verdicts[1]
	legacyDeliveryAppend(t, history, episodic.PlanState, loop.PlanState{PlanID: id, Title: p.Title, Steps: sup.Steps, Next: 0, Blocked: -1, RevisionTarget: -1})
	before, err := wr.Events()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	checks = nil
	mu.Unlock()
	status, response := f.post(t, "commands", map[string]any{"command_id": "recover-legacy", "kind": "step", "step": -1, "continue_plan": true, "plan_event_id": id})
	if status != http.StatusAccepted {
		t.Fatalf("Recover rejected: status=%d response=%s", status, response)
	}
	finished := legacyDeliveryWait(t, f)
	if finished.Status != "completed" || finished.Result == nil || finished.Result.StopReason != loop.StopFinalAnswer {
		t.Fatalf("Recover did not complete: %+v", finished)
	}
	state, err := f.l.PlanStateOf(f.sid)
	if err != nil || !state.Done || state.PlanID != id {
		t.Fatalf("recovered plan: %+v err=%v", state, err)
	}
	if state.Steps[0].Attempts != 1 || state.Steps[1].Attempts != 1 || state.Steps[2].Attempts != 1 {
		t.Fatalf("recovery rebuilt a prior step or reset accounting: %+v", state.Steps)
	}
	for i, st := range state.Steps {
		if st.Status != "passed" || st.Verdict == nil || !st.Verdict.Pass || st.Verdict.Check != p.Steps[i].Verify.Describe() || st.Verdict.EvidenceEventID == "" || st.Rev != 0 {
			t.Fatalf("step %d lacks unchanged-check evidence: %+v", i, st)
		}
	}
	if state.Steps[0].Verdict.EvidenceEventID == verdicts[0].EvidenceEventID {
		t.Fatal("old foundation evidence was promoted without a fresh recheck")
	}
	afterPlan, afterID, err := loop.LatestPlan(f.a.sess.EventsPath(f.sid))
	if err != nil || afterID != id || !reflect.DeepEqual(afterPlan, p) || afterPlan.Delivery != nil {
		t.Fatalf("Recover changed the legacy plan's identity or contracts: id=%s err=%v", afterID, err)
	}
	after, err := wr.Events()
	if err != nil || len(after) <= len(before) || !reflect.DeepEqual(after[:len(before)], before) {
		t.Fatal("Recover rewrote earlier plan/check/failure evidence")
	}
	for _, ev := range after[len(before):] {
		if ev.Type == episodic.Plan {
			t.Fatal("Recover recommitted or remapped the legacy plan")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 3 || !strings.Contains(prompts[0], "STEP 2 of the committed plan: Lighting") || !strings.Contains(prompts[2], "STEP 3 of the committed plan: Integration") {
		t.Fatalf("Recover selected the wrong step or did not continue: model calls=%d", len(prompts))
	}
	if !strings.Contains(prompts[0], "DELIVERY ORDER") || !strings.Contains(prompts[0], verdicts[0].EvidenceEventID) || !strings.Contains(prompts[0], "TypeError") {
		t.Fatal("original delivery constraints or historical evidence missing from recovery prompt")
	}
	if len(checks) < 3 || checks[0] != "check-lighting" {
		t.Fatalf("Recover rechecked a stale prerequisite before targeting the failure: %v", checks)
	}
	foundation, integration := -1, -1
	for i, check := range checks {
		if check == "check-foundation" && foundation < 0 {
			foundation = i
		}
		if check == "check-integration" {
			integration = i
		}
	}
	if foundation < 0 || integration <= foundation {
		t.Fatalf("continued without first rechecking prior evidence: %v", checks)
	}
}

func TestHTTPRecoverInvalidDeliveryPreservesBlockedStateBeforeExecution(t *testing.T) {
	for _, invalid := range []string{"explicit null contract", "modern step ID without contract"} {
		t.Run(invalid, func(t *testing.T) {
			var modelCalls, toolCalls atomic.Int32
			f := newRunAPIFixture(t, func(w http.ResponseWriter, r *http.Request) {
				modelCalls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"must not execute"},"finish_reason":"stop"}]}`)
			})
			f.l.SetRegistry(tools.NewRegistry(tools.Entry{Tool: legacyDeliveryRecoveryTool{name: "bash", run: func(json.RawMessage) (string, error) {
				toolCalls.Add(1)
				return "unexpected tool invocation", fmt.Errorf("must not execute")
			}}, RiskTier: tools.RiskSafe}))
			wr, err := f.a.Writer(f.sid)
			if err != nil {
				t.Fatal(err)
			}
			legacyDeliveryAppend(t, wr, episodic.MsgUser, map[string]any{"text": "Build the fixture.\n## DELIVERY ORDER\n1. Repair lighting."})
			check := &plan.Verify{Kind: "contains", File: "world.txt", Symbol: "lighting repaired"}
			step := map[string]any{"title": "Lighting", "files": []string{"world.txt"}, "verify": check}
			rawPlan := map[string]any{"title": "Invalid delivery fixture", "steps": []any{step}}
			if invalid == "explicit null contract" {
				rawPlan["delivery_contract"] = nil
			} else {
				step["id"] = "step-lighting"
			}
			committed := legacyDeliveryAppend(t, wr, episodic.Plan, rawPlan)
			p, _, err := loop.LatestPlan(f.a.sess.EventsPath(f.sid))
			if err != nil {
				t.Fatal(err)
			}
			meta, err := f.a.sess.Get(f.sid)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(meta.Workspace, "world.txt"), []byte("lighting broken"), 0600); err != nil {
				t.Fatal(err)
			}
			// Even established matching historical execution must not let malformed
			// or modern unbound metadata masquerade as an old-schema plan.
			history := wr.Scoped(map[string]any{"session_id": f.sid, "run_id": "historical-harness-run"})
			legacyDeliveryAppend(t, history, episodic.Note, map[string]any{"kind": "verify_started", "index": 0, "verify": check})
			verdict := loop.RunVerify(context.Background(), nil, meta.Workspace, check)
			finished := legacyDeliveryAppend(t, history, episodic.Note, map[string]any{"kind": "verify_finished", "index": 0, "verdict": verdict})
			verdict.EvidenceEventID = finished.ID
			sup := loop.NewSupervisor(p)
			sup.Steps[0].Status, sup.Steps[0].Attempts, sup.Steps[0].Verdict = "blocked", 1, &verdict
			sup.Steps[0].Reason = "Retain the failed check until an authorized recovery can run."
			legacyDeliveryAppend(t, history, episodic.PlanState, loop.PlanState{PlanID: committed.ID, Title: p.Title, Steps: sup.Steps, Next: -1, Blocked: 0, RevisionTarget: -1})
			beforeState, err := f.l.PlanStateOf(f.sid)
			if err != nil {
				t.Fatal(err)
			}
			before, err := wr.Events()
			if err != nil {
				t.Fatal(err)
			}
			status, response := f.post(t, "commands", map[string]any{"command_id": "reject-invalid-delivery", "kind": "step", "step": -1, "continue_plan": true, "plan_event_id": committed.ID})
			if status != http.StatusAccepted {
				t.Fatalf("command lifecycle admission failed unexpectedly: status=%d response=%s", status, response)
			}
			run := legacyDeliveryWait(t, f)
			if run.Status != "failed" || !strings.Contains(run.Reason, "delivery") {
				t.Fatalf("invalid delivery did not fail closed: %+v", run)
			}
			afterState, err := f.l.PlanStateOf(f.sid)
			if err != nil || !reflect.DeepEqual(afterState, beforeState) {
				t.Fatalf("denied Recover reset blocked status, attempts, verdict or identity: before=%+v after=%+v err=%v", beforeState, afterState, err)
			}
			after, err := wr.Events()
			if err != nil || len(after) < len(before) || !reflect.DeepEqual(after[:len(before)], before) {
				t.Fatal("denied Recover changed historical evidence")
			}
			for _, ev := range after[len(before):] {
				switch ev.Type {
				case episodic.Plan, episodic.PlanState, episodic.ToolCall, episodic.ToolResult:
					t.Fatalf("denied Recover mutated plan state or executed a tool: %s", ev.Type)
				}
			}
			if modelCalls.Load() != 0 || toolCalls.Load() != 0 {
				t.Fatalf("denied Recover executed model=%d tools=%d", modelCalls.Load(), toolCalls.Load())
			}
		})
	}
}
