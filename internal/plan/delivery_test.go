package plan

import (
	"strings"
	"testing"
)

func TestDeliveryExplicitListsAndStructuredContract(t *testing.T) {
	for _, text := range []string{"Build this.\n## Delivery order\n1. Browser-playable slice\n2. Persistence", `{"delivery_contract":{"milestones":[{"id":"milestone-1","text":"Browser-playable slice"},{"id":"milestone-2","text":"Persistence"}]}}`} {
		c, err := ParseDelivery(text, "evt_000001")
		if err != nil || c.Status != "explicit_order" || len(c.Milestones) != 2 || c.SourceEventID != "evt_000001" || c.SourceSHA256 == "" {
			t.Fatalf("contract: %+v %v", c, err)
		}
		for _, steps := range [][]string{{"milestone-1", "milestone-2"}, {"milestone-1", "milestone-1", "milestone-2"}} {
			if err := ValidateDelivery(c, steps); err != nil {
				t.Fatal(err)
			}
		}
		for _, steps := range [][]string{{"milestone-2", "milestone-1"}, {"milestone-1"}, {"", "milestone-2"}, {"milestone-1", "milestone-2", "milestone-1"}} {
			if err := ValidateDelivery(c, steps); err == nil {
				t.Fatalf("accepted reordered, missing or unbound delivery: %v", steps)
			}
		}
	}
}

func TestDeliveryDoesNotInferAmbiguousProse(t *testing.T) {
	for _, text := range []string{"First make it playable then add persistence.", "Features:\n1. Browser\n2. Persistence", "Delivery should be in a sensible order."} {
		c, err := ParseDelivery(text, "")
		if err != nil || c.Status != "not_semantically_validated" || len(c.Milestones) != 0 {
			t.Fatalf("prose claimed validated: %+v %v", c, err)
		}
	}
	for _, text := range []string{"Delivery order:\n- browser\n- data", "Delivery milestones\n1. browser\n3. data", `{"delivery_contract":{"milestones":[{"id":"a","text":"One"},{"id":"a","text":"Two"}]}}`} {
		if _, err := ParseDelivery(text, ""); err == nil {
			t.Fatalf("accepted malformed explicit contract: %s", text)
		}
	}
}

func TestDeliveryRetainsMultilineListAndRejectsMultipleSections(t *testing.T) {
	c, err := ParseDelivery("Delivery order:\n1. Browser\n   User can move.\n\n2. Persistence\n   Retain worlds.\n\n## Constraints\nSafe.", "source")
	if err != nil || len(c.Milestones) != 2 || !strings.Contains(c.Milestones[0].Text, "User can move.") || !strings.Contains(c.Milestones[1].Text, "Retain worlds.") {
		t.Fatalf("truncated list: %+v %v", c, err)
	}
	if _, err := ParseDelivery("Delivery order:\n1. Browser\n\nOther prose\nDelivery milestones:\n1. Something else", "source"); err == nil {
		t.Fatal("ignored second explicit list")
	}
}
