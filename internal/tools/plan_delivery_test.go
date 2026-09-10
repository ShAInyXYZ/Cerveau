package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/plan"
)

func TestCommitPlanDeliveryAdmissionFromUserOnly(t *testing.T) {
	w, err := episodic.Open(t.TempDir() + "/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Append(episodic.MsgUser, map[string]string{"text": "Build.\nDelivery order:\n1. Play in browser\n2. Save worlds"})
	tool := NewCommitPlan(func(string) (*episodic.Writer, error) { return w, nil }, &SessionContext{SessionID: "s"})
	makeArgs := func(ids ...string) json.RawMessage {
		steps := []map[string]any{}
		for _, id := range ids {
			steps = append(steps, map[string]any{"title": "work", "files": []string{"app.js"}, "milestone_id": id, "verify": map[string]string{"kind": "contains", "file": "app.js", "symbol": "renderWorld"}})
		}
		raw, _ := json.Marshal(map[string]any{"title": "P", "steps": steps, "delivery_contract": map[string]any{"status": "not_semantically_validated"}})
		return raw
	}
	for _, ids := range [][]string{{"milestone-2", "milestone-1"}, {"milestone-1"}, {"", "milestone-2"}} {
		if _, err := tool.Execute(context.Background(), makeArgs(ids...)); err == nil {
			t.Fatalf("accepted invalid delivery %v", ids)
		}
	}
	events, _ := w.Events()
	if len(events) != 1 {
		t.Fatal("rejected plans mutated journal")
	}
	out, err := tool.Execute(context.Background(), makeArgs("milestone-1", "milestone-2"))
	if err != nil || !strings.Contains(out, "semantic coverage not validated") {
		t.Fatalf("%s %v", out, err)
	}
	events, _ = w.Events()
	var saved struct {
		Delivery *plan.DeliveryContract `json:"delivery_contract"`
		Steps    []planStepIn           `json:"steps"`
	}
	json.Unmarshal(events[len(events)-1].Payload, &saved)
	if saved.Delivery.Status != "explicit_order" || saved.Delivery.SourceEventID != events[0].ID || saved.Steps[0].ID != "step-1" || saved.Steps[1].ID != "step-2" {
		t.Fatalf("lost authentic contract/IDs: %+v", saved)
	}
}
