package loop

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// After a compaction the model has lost the turns that carried the ORIGINAL
// REQUEST. The marker left in their place says history went and suggests
// re-reading the workspace — but a file listing cannot tell it what it was
// asked to build, which is exactly the thing it needs most.
//
// The brief injects the facts that are not recoverable from disk: the goal, the
// plan, what has already been finished, and what is currently in the workspace.
// It is assembled from the episodic log, never from the model's own summary —
// asking a model that just lost context to summarise what it lost is circular.

type resumeFacts struct {
	Goal      string   // the user's original request
	Workspace string   // absolute path
	Files     []string // what is on disk right now
	Done      []string // completed steps / checkpoints
	Plan      string   // committed plan title, if any
	Compacted int      // how many turns were folded away
}

func buildResumeBrief(f resumeFacts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[CONTEXT COMPACTED — %d earlier turns were removed to stay under the "+
		"context limit. This brief replaces them. Everything below is fact from the "+
		"session log, not a guess.]\n\n", f.Compacted)

	if f.Goal != "" {
		fmt.Fprintf(&b, "ORIGINAL REQUEST:\n%s\n\n", strings.TrimSpace(f.Goal))
	}
	if f.Plan != "" {
		fmt.Fprintf(&b, "PLAN: %s\n\n", f.Plan)
	}
	// Only claim completed work when there is some. A model told "completed:
	// (none)" reads it as "start over", which is the opposite of the truth when
	// the log simply has no checkpoints.
	if len(f.Done) > 0 {
		b.WriteString("ALREADY COMPLETED — do NOT redo these:\n")
		for _, d := range f.Done {
			fmt.Fprintf(&b, "  - %s\n", d)
		}
		b.WriteString("\n")
	}
	if f.Workspace != "" {
		fmt.Fprintf(&b, "WORKSPACE: %s\n", f.Workspace)
	}
	if len(f.Files) > 0 {
		fmt.Fprintf(&b, "FILES ON DISK NOW: %s\n", strings.Join(f.Files, ", "))
		b.WriteString("These are the current state of the work — read them before " +
			"changing anything, and continue from what they already contain rather " +
			"than rewriting from scratch.\n")
	}
	b.WriteString("\nContinue the task. Do not restart it, and do not ask the user to " +
		"repeat the request — it is stated above.")
	return b.String()
}

// workspaceFiles lists the files a resuming model should know about: source and
// markup at the top level. Directories and dotfiles are skipped — a node_modules
// listing would bury the three files that matter.
func workspaceFiles(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".html", ".js", ".mjs", ".ts", ".css", ".json", ".py", ".go", ".md", ".txt":
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	if len(out) > 25 {
		out = append(out[:25], fmt.Sprintf("...and %d more", len(out)-25))
	}
	return out
}
