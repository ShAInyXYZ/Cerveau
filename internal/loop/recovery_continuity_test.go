package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
	"cerveau/internal/tools"
)

func runCoverageAttempt(t *testing.T, m *scriptedModel, recovering bool) (string, error, []episodic.Event) {
	t.Helper()
	l, journal := gateFixture(t, m)
	ws := t.TempDir()
	l.SetWorkspaceFunc(func(string) string { return ws })
	var content strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&content, "source line %d: %s\n", i, strings.Repeat("context", 30))
	}
	if err := os.WriteFile(filepath.Join(ws, "world.js"), []byte(content.String()), 0600); err != nil {
		t.Fatal(err)
	}
	l.SetRegistry(tools.NewRegistry(tools.Entry{Tool: tools.NewRead(ws), RiskTier: tools.RiskSafe}, tools.Entry{Tool: tools.NewEdit(ws), RiskTier: tools.RiskSensitive}))
	p := &Plan{Title: "source coverage", Steps: []PlanStep{{Title: "repair", Files: []string{"world.js"}}}}
	s := NewSupervisor(p)
	if recovering {
		s.Steps[0].Reason = "recorded failure"
	}
	ctx, h, finish, err := l.beginRun(context.Background(), "s1", "autopilot", "")
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil, nil)
	h.registry = l.registry()
	if _, err = h.writer.Append(episodic.Plan, p); err != nil {
		t.Fatal(err)
	}
	ctx, brief, err := l.prepareRecovery(ctx, "s1", p, s, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := l.runStep(ctx, h.writer, "s1", "Inspect and repair only the current step.", ModeByName("autopilot"), p, 0, nil, StepPrompt{Step: p.Steps[0], Context: brief})
	events, err := episodic.Replay(journal)
	if err != nil {
		t.Fatal(err)
	}
	return out, runErr, events
}

func TestRecoverySourceContinuityKeepsWindowForSupportedRepair(t *testing.T) {
	var replies []map[string]any
	for i := 1; i <= 8; i++ {
		replies = append(replies, toolCall("read", fmt.Sprintf(`{"path":"world.js","from_line":%d,"to_line":%d}`, i, i)))
	}
	replies = append(replies, toolCall("edit", `{"path":"world.js","old_string":"source line 1:","new_string":"repaired line 1:"}`), textReply("ready for check"))
	m := newScriptedModel(replies...)
	defer m.srv.Close()
	out, err, events := runCoverageAttempt(t, m, true)
	if err != nil || out != "ready for check" {
		t.Fatalf("lost same-window repair: %q %v", out, err)
	}
	if len(m.bodies) != 10 || !strings.Contains(m.bodies[8], "RECOVERY SOURCE CONTINUATION") || !strings.Contains(m.bodies[8], "source line 1:") {
		t.Fatal("window/continuation lost")
	}
	count := 0
	for _, ev := range events {
		if ev.Type == episodic.Note && strings.Contains(string(ev.Payload), `"kind":"recovery_source_continuation"`) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("continuations=%d", count)
	}
}

func TestRecoverySourceContinuityIsBoundedAndRecoveryOnly(t *testing.T) {
	for _, recovering := range []bool{false, true} {
		t.Run(fmt.Sprintf("recovering=%v", recovering), func(t *testing.T) {
			var replies []map[string]any
			for i := 1; i <= 25; i++ {
				replies = append(replies, toolCall("read", fmt.Sprintf(`{"path":"world.js","from_line":%d,"to_line":%d}`, i, i)))
			}
			m := newScriptedModel(replies...)
			defer m.srv.Close()
			_, err, _ := runCoverageAttempt(t, m, recovering)
			want := 8
			if recovering {
				want = 16
			}
			if err == nil || !strings.Contains(err.Error(), "iteration cap") || len(m.bodies) != want {
				t.Fatalf("unbounded/wrong phase: calls%d want%d err%v", len(m.bodies), want, err)
			}
		})
	}
}
