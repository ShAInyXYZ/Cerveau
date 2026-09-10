package tools

import (
	"strings"
	"testing"
)

func TestCommitPlanGuidanceRequiresStepCoverage(t *testing.T) {
	tool := commitPlanTool(t)
	for _, want := range []string{"all behavior claimed", "title and detail", "split the step", "integration", "explicit delivery order", "early runnable/observable milestones", "original request"} {
		if got := tool.Description(); !strings.Contains(got, want) {
			t.Errorf("commit_plan guidance missing %q: %s", want, got)
		}
	}
	properties := tool.Schema()["properties"].(map[string]any)["steps"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	verify := properties["verify"].(map[string]any)
	if got, _ := verify["description"].(string); !strings.Contains(got, "all behavior claimed in the step's title and detail") {
		t.Errorf("schema guidance lost step coverage requirement: %s", got)
	}
}
