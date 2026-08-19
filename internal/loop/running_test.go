package loop

import "testing"

// A run started from the CLI is invisible in the panel: the session appears in
// the list like any other, with no sign that a turn is executing inside it
// right now. The user watching the WebUI sees nothing happening while the
// machine builds for thirty minutes.
//
// The registry already knows — it is what Steer/Pause/Kill use. It is simply
// never exposed.
func TestRunningSessionsAreReportable(t *testing.T) {
	l := &Loop{runs: newRunsRegistry()}
	if got := l.RunningSessions(); len(got) != 0 {
		t.Fatalf("nothing started, got %v", got)
	}

	done := l.runs.register("sess-a", &runHandle{})
	defer done()
	l.runs.register("sess-b", &runHandle{})

	got := l.RunningSessions()
	if len(got) != 2 {
		t.Fatalf("expected 2 running sessions, got %d: %v", len(got), got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen["sess-a"] || !seen["sess-b"] {
		t.Errorf("missing a session: %v", got)
	}
}

// A finished run must drop off immediately, or the UI shows a permanent
// phantom "running" badge.
func TestFinishedRunsDisappear(t *testing.T) {
	l := &Loop{runs: newRunsRegistry()}
	done := l.runs.register("sess-a", &runHandle{})
	done()
	if got := l.RunningSessions(); len(got) != 0 {
		t.Errorf("finished run still reported: %v", got)
	}
}
