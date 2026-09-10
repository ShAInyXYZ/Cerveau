package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"cerveau/internal/plan"
	"cerveau/internal/tools"
)

// Verdict is what a step's verify actually observed.
//
// Evidence is the point: "done" must be something the harness watched happen,
// with the output that says so, not the model's opinion that it has finished.
type Verdict struct {
	VerificationReview    *VerificationReview `json:"verification_review,omitempty"`
	WorkspaceVersion      string              `json:"workspace_version,omitempty"`
	EvidenceEventID       string              `json:"evidence_event_id,omitempty"`
	DeclaredSourceVersion string              `json:"declared_source_version,omitempty"`
	ExecutionStop         string              `json:"execution_stop,omitempty"`
	FailureKind           string              `json:"failure_kind,omitempty"`
	Pass                  bool                `json:"pass"`
	Check                 string              `json:"check"`    // the verify, in one line
	Evidence              string              `json:"evidence"` // what came back
}

// verifyToolRunner is the slice of the tool registry a verify needs. Narrow on
// purpose: verification runs the step's declared check and nothing else.
type verifyToolRunner interface {
	ExecuteMode(ctx context.Context, name string, args json.RawMessage, mode string) (string, error)
}

// Chromium's console renderer leaves its closing quote and source location on
// check_page's eval line. Match the complete value (not a truthy prefix) and
// only that known suffix; arbitrary trailing text must never prove a check.
var verifyEvalValue = regexp.MustCompile(`^(true|false|"true"|"false")(?:", source: .+ \([0-9]+\))?$`)

// RunVerify performs the step's declared check and reports what it saw.
//
// Every kind fails CLOSED: an error running the check is not a pass. A step
// whose verify cannot run is exactly as unproven as one whose verify failed,
// and treating "could not check" as success is how a plan goes green while
// nothing works.
func RunVerify(ctx context.Context, reg verifyToolRunner, workspace string, v *plan.Verify) Verdict {
	if v == nil {
		return Verdict{Pass: false, Check: "none", Evidence: "the step declared no verify, so nothing proves it done"}
	}
	line := v.Describe()
	switch strings.ToLower(strings.TrimSpace(v.Kind)) {
	case "eval":
		return verifyEval(ctx, reg, v, line)
	case "command":
		return verifyCommand(ctx, reg, v, line)
	case "contains":
		return verifyContains(workspace, v, line)
	}
	return Verdict{Pass: false, Check: line, Evidence: "unknown verify kind " + v.Kind}
}

func verifyEval(ctx context.Context, reg verifyToolRunner, v *plan.Verify, line string) Verdict {
	// Ask for the value of the expression, JSON-encoded, so "truthy" is decided
	// here rather than by however the page chose to print it.
	// Await first: a Promise object is truthy even when it resolves to false.
	args := map[string]any{"eval": "Promise.resolve((" + v.Expr + ")).then(value => JSON.stringify(!!value))"}
	if v.Path != "" {
		args["path"] = v.Path
	} else {
		args["url"] = v.URL
	}
	raw, _ := json.Marshal(args)
	out, err := reg.ExecuteMode(ctx, "check_page", raw, "autopilot")
	// A partial true value cannot override a timed-out or failed browser.
	// Retain output as well as the error: loading stages and stderr explain
	// whether to diagnose the application, its server, or the probe itself.
	kind := browserFailureKind(out)
	if err != nil || kind != "" {
		evidence := out
		if err != nil {
			evidence += "\ncheck_page failed: " + err.Error()
		}
		if kind == "" {
			kind = "browser_unavailable"
		}
		return Verdict{Pass: false, Check: line, FailureKind: kind, Evidence: verificationEvidence(evidence)}
	}
	// check_page reports the value as `eval result: <v>` among console lines.
	pass := false
	for _, l := range strings.Split(out, "\n") {
		if val, ok := strings.CutPrefix(strings.TrimSpace(l), "eval result:"); ok {
			m := verifyEvalValue.FindStringSubmatch(strings.TrimSpace(val))
			pass = len(m) == 2 && (m[1] == "true" || m[1] == `"true"`)
			break
		}
	}
	return Verdict{Pass: pass, Check: line, Evidence: verificationEvidence(out)}
}

func browserFailureKind(out string) string {
	d, ok := tools.ParseBrowserDiagnostics(out)
	if !ok {
		return ""
	}
	switch d.Status {
	case "timeout", "cancelled", "process_failed", "missing_dom", "missing_eval", "eval_timeout":
		return "browser_" + d.Status
	}
	return ""
}

func verifyCommand(ctx context.Context, reg verifyToolRunner, v *plan.Verify, line string) Verdict {
	raw, _ := json.Marshal(map[string]any{"command": v.Command})
	out, err := reg.ExecuteMode(ctx, "bash", raw, "autopilot")
	if err != nil {
		// A non-zero exit surfaces as an error here — which is the whole point
		// of this kind, so report it as a clean failure with its output.
		return Verdict{Pass: false, Check: line, Evidence: verificationEvidence(strings.TrimSpace(out + "\n" + err.Error()))}
	}
	return Verdict{Pass: true, Check: line, Evidence: verificationEvidence(out)}
}

func verificationEvidence(out string) string {
	// Long node -e source/caret lines used to consume the entire 600-byte
	// prefix and discard the actual error. Keep both ends; full tool output
	// remains in the journal under Verdict.EvidenceEventID.
	return recoveryExcerpt(strings.TrimSpace(out), 3600)
}

func verifyContains(workspace string, v *plan.Verify, line string) Verdict {
	root, _ := filepath.Abs(workspace)
	full := filepath.Join(root, filepath.Clean("/"+v.File))
	resolved, resolveErr := filepath.EvalSymlinks(full)
	if resolveErr != nil {
		return Verdict{Check: line, Evidence: resolveErr.Error()}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil || !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return Verdict{Check: line, Evidence: "check path escapes session workspace"}
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return Verdict{Pass: false, Check: line, Evidence: fmt.Sprintf("%s: %v", v.File, err)}
	}
	if strings.Contains(string(b), v.Symbol) {
		return Verdict{Pass: true, Check: line, Evidence: fmt.Sprintf("%s contains %q (%d bytes)", v.File, v.Symbol, len(b))}
	}
	return Verdict{Pass: false, Check: line,
		Evidence: fmt.Sprintf("%s does not contain %q — the file is %d bytes, so it exists but this step's work is not in it",
			v.File, v.Symbol, len(b))}
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
