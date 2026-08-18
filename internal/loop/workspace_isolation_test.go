package loop

import (
	"strings"
	"testing"
)

// A session carries its OWN workspace. The harness tells the model
// "All file tools are rooted here" in envBlock — that sentence must be TRUE.
//
// Observed 2026-08-18 during the harness benchmark: a session created with
// workspace ~/Pictures/Benchmark/... had its tools run against the CORE's
// global workspace (~/Pictures/GameTest/chess) instead. The static server it
// started served the wrong project, and a regex search reported "no matches"
// for a function that existed in the session's real workspace.
//
// The prompt said one path; the tools used another.
func TestEnvBlockReportsTheSessionWorkspace(t *testing.T) {
	l := &Loop{}
	l.SetWorkspaceFunc(func(sessionID string) string {
		if sessionID == "s-bench" {
			return t.TempDir() // a session-specific workspace
		}
		return "/global/core/workspace"
	})

	env := l.envBlock("s-bench")
	if env == "" {
		t.Fatal("envBlock empty for a session with a workspace")
	}
	if strings.Contains(env, "/global/core/workspace") {
		t.Errorf("envBlock leaked the GLOBAL workspace into a session prompt:\n%s", env)
	}
}
