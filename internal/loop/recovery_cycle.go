package loop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"cerveau/internal/plan"
)

// A recovery cycle is a small repair batch followed by the unchanged committed
// check. Source churn is not success. A newly observed checked failure can buy
// one bounded diagnostic slice, but never resets the failed-cycle limit.
type recoveryCycle struct {
	checkedVersion string
	seen           map[string]bool
	changes        int
	dirtySince     int
	failedCycles   int
	credit         bool
}

func newRecoveryCycle(version string) *recoveryCycle {
	return &recoveryCycle{checkedVersion: version, seen: map[string]bool{}}
}

func (c *recoveryCycle) repair(version string, round int) {
	if version == c.checkedVersion {
		return
	}
	if c.changes == 0 {
		c.dirtySince = round
	}
	c.changes++
}

func (c *recoveryCycle) due(version string, round int, boundary bool) bool {
	return version != c.checkedVersion && c.changes > 0 && (c.changes >= 2 || round-c.dirtySince >= 4 || boundary)
}

func (c *recoveryCycle) checked(version string, v Verdict, baseline bool) {
	// The contains check's byte count is metadata, not a different failure.
	failure := v.Evidence
	if at := strings.Index(failure, " — the file is "); at >= 0 {
		failure = failure[:at]
	}
	sig := recoveryFailureIdentity(failure)
	c.credit = !baseline && version != c.checkedVersion && !c.seen[sig]
	c.seen[sig] = true
	c.checkedVersion = version
	c.changes, c.dirtySince = 0, 0
	if !baseline && !v.Pass {
		c.failedCycles++
	}
}

var recoveryStackFrame = regexp.MustCompile(`^at\s+(?:(.*?)\s+\()?((?:file://)?[^()]+?):\d+(?::\d+)?\)?$`)

// A new failing function is useful evidence even when the exception headline
// is unchanged. Line/column shifts, source hashes and passing-test chatter are
// not: editing whitespace must not buy another diagnostic slice. Keep only two
// non-runtime frames (usually the failing function and its caller/test).
func recoveryFailureIdentity(evidence string) string {
	headline := errorLine(evidence)
	if headline == "" {
		return recoverySHA([]byte(evidence))
	}
	parts := []string{headline}
	for _, line := range strings.Split(evidence, "\n") {
		frame := recoveryStackFrame.FindStringSubmatch(strings.TrimSpace(line))
		if frame == nil || strings.HasPrefix(frame[2], "node:") || strings.Contains(frame[2], "node:internal/") {
			continue
		}
		parts = append(parts, strings.TrimSpace(frame[1])+"@"+filepath.ToSlash(frame[2]))
		if len(parts) == 3 {
			break
		}
	}
	return recoverySHA([]byte(strings.Join(parts, "\n")))
}

func (c *recoveryCycle) takeCredit() bool {
	credit := c.credit
	c.credit = false
	return credit
}

func (c *recoveryCycle) exhausted() bool { return c.failedCycles >= 3 }

// Content identity of declared, bounded source only. Touching a file, changing
// an unrelated diagnostic artifact, or appending the journal cannot earn a
// repair budget. Missing/oversized paths are explicitly not verified source.
func recoveryStepVersion(workspace string, step PlanStep) string {
	paths := append([]string(nil), step.Files...)
	if step.Verify != nil && step.Verify.File != "" {
		paths = append(paths, step.Verify.File)
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, path := range paths {
		path = filepath.Clean(path)
		fmt.Fprintf(&b, "%s\x00%s\n", path, recoveryCurrentSHA(workspace, path))
	}
	return recoverySHA([]byte(b.String()))
}

func recoveryCheckPrompt(v Verdict, verify *plan.Verify, baseline bool) string {
	raw, _ := json.Marshal(v)
	check, _ := json.Marshal(verify)
	label := "RECOVERY CHECKPOINT RESULT"
	if baseline {
		label = "CURRENT RECOVERY BASELINE"
	}
	label += "\nIf evidence shows a generated criterion conflicts with the task, use read_plan_step to inspect related contracts and request_verification_review to hand back the discrepancy. Do not change the fixture or metrics merely to satisfy that criterion."
	label += browserRecoveryGuidance(v)
	return label + " — harness-executed unchanged committed check, newest evidence for this source version. Earlier failures are historical and may no longer apply.\n" + string(raw) + "\nCommitted check (do not change): " + string(check) + "\nBefore the next repair, state one specific hypothesis, the source/evidence supporting it, and what this check or one targeted probe should show if it is correct. If a repair left the same failure, change the hypothesis; do not keep mutating the implementation without checking. Use narrow source reads and small coherent repair batches. A changed failure is new evidence, not proof of improvement. After at most two source-changing tool calls (finishing the current tool group), the harness checks again. Three failed repair/check cycles hand back for a decision.\n"
}

func browserRecoveryGuidance(v Verdict) string {
	if !strings.HasPrefix(v.FailureKind, "browser_") {
		return ""
	}
	return "\nBROWSER VERIFICATION UNAVAILABLE: the browser did not complete this check; this is not evidence that the committed expression evaluated false. Preserve the expression. Inspect the retained browser status, loading stages and stderr. Use native serve action=list, then action=probe with the owned port and path to check HTTP status, content type and served-file identity. Recovery bash cannot reach the host's network; do not repeat curl there or relax its sandbox. If HTTP succeeds but eval_started is absent, investigate synchronous initialization/frame work or browser startup rather than guessing a selector fix. A trivial eval still runs on the same page thread and can stall behind it. Distinguish a browser/process failure from an application runtime error; use one targeted probe to test the hypothesis. Missing browser evidence remains unverified and does not grant an unlimited retry budget."
}

type recoveryCycleStop struct{ detail string }

func (e *recoveryCycleStop) Error() string { return "recovery checkpoint: " + e.detail }

// Keep the latest check output authoritative. Execution/budget diagnostics
// have a separate field; concatenating them formerly made old failures look
// current and buried the exact assertion the next retry actually must fix.
func withExecutionStop(v *Verdict, err error) {
	if err != nil {
		v.ExecutionStop = err.Error()
	}
}
