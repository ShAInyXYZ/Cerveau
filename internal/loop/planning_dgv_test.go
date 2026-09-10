package loop

import (
	"context"
	"encoding/json"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/tools"
)

type planningLookupTool struct {
	name  string
	calls int
}

func (t *planningLookupTool) Name() string        { return t.name }
func (t *planningLookupTool) Description() string { return "planning lookup fixture" }
func (t *planningLookupTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *planningLookupTool) Execute(context.Context, json.RawMessage) (string, error) {
	t.calls++
	return "lookup: " + t.name, nil
}

func TestPlanGateOffersAndDispatchesBoundedDGVReads(t *testing.T) {
	names := []string{"dgv-list", "dgv-catalog", "dgv-context", "dgv-read", "dgv-check"}
	var replies []map[string]any
	for _, name := range names[:maxPlanReads] {
		replies = append(replies, toolCall(name, `{}`))
	}
	replies = append(replies, toolCall("commit_plan", `{"title":"P","steps":[{"title":"one","files":["index.html"],"verify":{"kind":"contains","file":"index.html","symbol":"monolith"}}]}`), textReply("done"))
	m := newScriptedModel(replies...)
	defer m.srv.Close()
	l, path := gateFixture(t, m)
	entries := l.registry().Entries()
	lookups := map[string]*planningLookupTool{}
	for _, name := range append(names, "dgv-create", "dgv-update", "arbitrary-safe-exec") {
		lookup := &planningLookupTool{name: name}
		lookups[name] = lookup
		entries = append(entries, tools.Entry{Tool: lookup, RiskTier: tools.RiskSafe})
	}
	l.SetRegistry(tools.NewRegistry(entries...))
	if _, err := l.Run(context.Background(), "s1", "build from existing DGV context", "autopilot"); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !hasTool(m.offered[0], name) {
			t.Errorf("planning must offer installed safe lookup %s: %v", name, m.offered[0])
		}
	}
	for _, name := range []string{"dgv-create", "dgv-update", "arbitrary-safe-exec"} {
		if hasTool(m.offered[0], name) {
			t.Errorf("planning must not expose side-effect tool %s", name)
		}
	}
	for _, name := range names[:maxPlanReads] {
		if lookups[name].calls != 1 {
			t.Errorf("offering and dispatch must agree: %s calls=%d", name, lookups[name].calls)
		}
	}
	if len(m.offered) <= maxPlanReads || len(m.offered[maxPlanReads]) != 1 || m.forced[maxPlanReads] != "commit_plan" {
		t.Fatalf("DGV reads must spend bounded planning budget: offered=%v forced=%v", m.offered, m.forced)
	}
	events, err := episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type != episodic.ToolResult {
			continue
		}
		var p struct {
			Name string
			OK   bool
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if hasTool(names[:maxPlanReads], p.Name) && !p.OK {
			t.Errorf("DGV read rejected in journal: %s", e.Payload)
		}
	}
}

func TestPlanGateDoesNotTrustDGVNameAlone(t *testing.T) {
	for _, tc := range []struct {
		name, risk string
		modes      []string
	}{
		{"dgv-context", tools.RiskSensitive, nil},
		{"dgv-catalog", tools.RiskDangerous, nil},
		{"dgv-read", tools.RiskSafe, []string{tools.ModeDiscussion}},
		{"dgv-create", tools.RiskSafe, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newScriptedModel(toolCall(tc.name, `{}`), textReply("done"))
			defer m.srv.Close()
			l, _ := gateFixture(t, m)
			lookup := &planningLookupTool{name: tc.name}
			l.SetRegistry(tools.NewRegistry(append(l.registry().Entries(), tools.Entry{Tool: lookup, RiskTier: tc.risk, Modes: tc.modes})...))
			if _, err := l.Run(context.Background(), "s1", "build from DGV", "autopilot"); err != nil {
				t.Fatal(err)
			}
			if hasTool(m.offered[0], tc.name) || lookup.calls != 0 {
				t.Fatalf("unsafe, unavailable or writing DGV tool escaped planning fence: offered=%v calls=%d", m.offered[0], lookup.calls)
			}
		})
	}
}
