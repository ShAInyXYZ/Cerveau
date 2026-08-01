package tools

import (
	"context"
	"encoding/json"
	"testing"

	"cerveau/internal/guard"
)

// A human-approved context passes SENSITIVE guard denials (the approval IS
// the confirmation the tier asks for). Catastrophic stays blocked, always.
func TestHumanApprovalPassesSensitiveOnly(t *testing.T) {
	reg := NewRegistry(Entry{Tool: &dryStub{name: "bash"}, RiskTier: RiskSafe})
	reg.SetGuard(func(tool string, args json.RawMessage) error {
		var a struct {
			Command string `json:"command"`
		}
		json.Unmarshal(args, &a)
		switch a.Command {
		case "git push":
			return &guard.TierError{Tier: guard.TierSensitive, Reason: "push", Hint: "confirm"}
		case "rm -rf /":
			return &guard.TierError{Tier: guard.TierCatastrophic, Reason: "rm", Hint: "never"}
		}
		return nil
	})

	push := json.RawMessage(`{"command":"git push"}`)
	if _, err := reg.ExecuteMode(context.Background(), "bash", push, ""); err == nil {
		t.Fatal("sensitive must be blocked WITHOUT approval")
	}
	if _, err := reg.ExecuteMode(WithHumanApproval(context.Background()), "bash", push, ""); err != nil {
		t.Fatalf("sensitive must pass WITH approval, got %v", err)
	}
	boom := json.RawMessage(`{"command":"rm -rf /"}`)
	if _, err := reg.ExecuteMode(WithHumanApproval(context.Background()), "bash", boom, ""); err == nil {
		t.Fatal("catastrophic must stay blocked even with approval")
	}
}
