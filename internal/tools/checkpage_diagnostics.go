package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

const browserDiagnosticsPrefix = "browser diagnostics: "
const pageStageMarker = "__CRV_STAGE__"

// BrowserDiagnostics separates missing verification from a false application
// assertion. Stages are observations from the injected harness, not proof of
// rendered terrain or responsive interaction.
type BrowserDiagnostics struct {
	Status          string   `json:"status"`
	Browser         string   `json:"browser,omitempty"`
	ElapsedMS       int64    `json:"elapsed_ms"`
	TimeoutMS       int64    `json:"timeout_ms"`
	DOMObserved     bool     `json:"dom_observed"`
	EvalRequested   bool     `json:"eval_requested"`
	EvalObserved    bool     `json:"eval_observed"`
	Stages          []string `json:"stages,omitempty"`
	LastStage       string   `json:"last_stage,omitempty"`
	OutputTruncated bool     `json:"output_truncated,omitempty"`
}

func ParseBrowserDiagnostics(out string) (BrowserDiagnostics, bool) {
	for _, line := range strings.Split(out, "\n") {
		if raw, ok := strings.CutPrefix(line, browserDiagnosticsPrefix); ok {
			var d BrowserDiagnostics
			if json.Unmarshal([]byte(raw), &d) == nil && d.Status != "" {
				return d, true
			}
		}
	}
	return BrowserDiagnostics{}, false
}

func browserStages(stderr string) []string {
	known := []string{"harness_installed", "dom_content_loaded", "window_loaded", "eval_started", "eval_finished", "eval_timed_out"}
	seen := map[string]bool{}
	var stages []string
	for _, line := range strings.Split(stderr, "\n") {
		at := strings.Index(line, pageStageMarker+" ")
		if at < 0 {
			continue
		}
		value := strings.TrimSpace(line[at+len(pageStageMarker)+1:])
		for _, stage := range known {
			if strings.HasPrefix(value, stage) && !seen[stage] {
				stages = append(stages, stage)
				seen[stage] = true
				break
			}
		}
	}
	return stages
}

func hasBrowserStage(stages []string, wanted string) bool {
	for _, stage := range stages {
		if stage == wanted {
			return true
		}
	}
	return false
}

func browserIncompleteHint(d BrowserDiagnostics) string {
	switch d.Status {
	case "timeout":
		return fmt.Sprintf("Browser wall-clock deadline reached after %d ms. Partial output is not a completed check. The page, a resource, or the evaluation may be stalled; absence of console errors does not prove clean loading. Use serve action=probe for the owned server. If HTTP succeeds but the probe timer never runs, investigate synchronous startup/frame work before changing the assertion.", d.ElapsedMS)
	case "cancelled":
		return "Browser check cancelled; no completed verification is available."
	case "process_failed":
		return "Browser process failed; inspect the retained process diagnostics before modifying application code."
	case "missing_dom":
		return "Browser exited without a rendered HTML body; loading is unverified. Check the owned server with serve action=probe and inspect browser diagnostics."
	case "missing_eval":
		return "Browser returned DOM but no evaluation receipt. This is missing evidence, not a false assertion or proof that the expression threw. Check loading stages and initialization before retrying."
	case "eval_timeout":
		return "The evaluation's Promise did not settle within its 4-second budget. This is incomplete evaluation, not a false assertion. Inspect the awaited operation and application readiness; keep the committed expression unchanged."
	}
	return ""
}
