package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cerveau/internal/plan"
)

// Verdict is what a step's verify actually observed.
//
// Evidence is the point: "done" must be something the harness watched happen,
// with the output that says so, not the model's opinion that it has finished.
type Verdict struct {
	Pass     bool   `json:"pass"`
	Check    string `json:"check"`    // the verify, in one line
	Evidence string `json:"evidence"` // what came back
}

// verifyToolRunner is the slice of the tool registry a verify needs. Narrow on
// purpose: verification runs the step's declared check and nothing else.
type verifyToolRunner interface {
	ExecuteMode(ctx context.Context, name string, args json.RawMessage, mode string) (string, error)
}

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
	args := map[string]any{"eval": "JSON.stringify(!!(" + v.Expr + "))"}
	if v.Path != "" {
		args["path"] = v.Path
	} else {
		args["url"] = v.URL
	}
	raw, _ := json.Marshal(args)
	out, err := reg.ExecuteMode(ctx, "check_page", raw, "autopilot")
	if err != nil {
		return Verdict{Pass: false, Check: line, Evidence: "check_page failed: " + err.Error()}
	}
	// check_page reports the value as `eval result: <v>` among console lines.
	pass := false
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "eval result:"); i >= 0 {
			val := strings.TrimSpace(l[i+len("eval result:"):])
			val = strings.TrimSuffix(strings.TrimSpace(val), `"`)
			pass = strings.HasPrefix(val, "true")
			break
		}
	}
	return Verdict{Pass: pass, Check: line, Evidence: clip(out, 600)}
}

func verifyCommand(ctx context.Context, reg verifyToolRunner, v *plan.Verify, line string) Verdict {
	raw, _ := json.Marshal(map[string]any{"command": v.Command})
	out, err := reg.ExecuteMode(ctx, "bash", raw, "autopilot")
	if err != nil {
		// A non-zero exit surfaces as an error here — which is the whole point
		// of this kind, so report it as a clean failure with its output.
		return Verdict{Pass: false, Check: line, Evidence: clip(strings.TrimSpace(out+"\n"+err.Error()), 600)}
	}
	return Verdict{Pass: true, Check: line, Evidence: clip(out, 600)}
}

func verifyContains(workspace string, v *plan.Verify, line string) Verdict {
	full := filepath.Join(workspace, filepath.Clean("/"+v.File))
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
