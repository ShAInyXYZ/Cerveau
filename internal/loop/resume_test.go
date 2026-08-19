package loop

import (
	"strings"
	"testing"
)

// After compaction the model knows history WENT, but not how to pick up. The
// marker says "re-read the files" — which is a hint, not a briefing. A model
// that lost the original request cannot re-derive it from a file listing.
//
// The brief has to carry the things that are NOT recoverable by reading the
// workspace: the original goal, the plan, what is already done, and what the
// files on disk currently are.
func TestResumeBriefCarriesTheGoal(t *testing.T) {
	b := buildResumeBrief(resumeFacts{
		Goal:      "Build a realistic home standing fan in 3D using Three.js",
		Workspace: "/home/shiny/Pictures/Benchmark/v11",
		Files:     []string{"index.html", "fan.js"},
		Done:      []string{"scaffolded index.html", "built the blade geometry"},
		Compacted: 29,
	})

	if !strings.Contains(b, "standing fan") {
		t.Error("the original goal is missing — the model cannot recover it by reading files")
	}
	if !strings.Contains(b, "index.html") {
		t.Error("the file list is missing")
	}
	if !strings.Contains(b, "blade geometry") {
		t.Error("completed work is missing — the model will redo it")
	}
	if !strings.Contains(b, "29") {
		t.Error("the compaction count is missing")
	}
}

// With nothing known, the brief must not invent structure or claim work was
// done. An empty section is better than a confident lie.
func TestResumeBriefDegradesHonestly(t *testing.T) {
	b := buildResumeBrief(resumeFacts{Goal: "do a thing", Workspace: "/tmp/w", Compacted: 3})
	if strings.Contains(strings.ToLower(b), "completed:") {
		t.Errorf("claimed completed work when none is known:\n%s", b)
	}
	if !strings.Contains(b, "do a thing") {
		t.Error("goal missing")
	}
}
