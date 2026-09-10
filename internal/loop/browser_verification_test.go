package loop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/plan"
	"cerveau/internal/tools"
)

func TestBrowserIncompleteVerificationRetainsEvidenceAndCannotPass(t *testing.T) {
	for _, status := range []string{"timeout", "cancelled", "process_failed", "missing_dom", "missing_eval", "eval_timeout"} {
		for _, toolErr := range []error{nil, errors.New("browser unavailable")} {
			t.Run(status+"/"+stringBool(toolErr != nil), func(t *testing.T) {
				out := `browser diagnostics: {"status":"` + status + `","last_stage":"harness_installed"}` + "\neval result: true\nprocess evidence preserved"
				reg := &fakeReg{out: out, err: toolErr}
				check := &plan.Verify{Kind: "eval", URL: "http://localhost:8000/index.html", Expr: "window.__world && document.querySelector('#hud')"}
				before, _ := json.Marshal(check)
				v := RunVerify(context.Background(), reg, "/unused", check)
				after, _ := json.Marshal(check)
				if v.Pass || v.FailureKind != "browser_"+status || !strings.Contains(v.Evidence, "harness_installed") || !strings.Contains(v.Evidence, "process evidence preserved") {
					t.Fatalf("incomplete browser check lost evidence or passed: %+v", v)
				}
				if string(before) != string(after) || v.Check != check.Describe() {
					t.Fatal("browser failure changed the committed check")
				}
				prompt := recoveryCheckPrompt(v, check, true)
				for _, need := range []string{"VERIFICATION UNAVAILABLE", "action=probe", "Preserve the expression", "same page thread", "unlimited retry"} {
					if !strings.Contains(prompt, need) {
						t.Fatalf("missing recovery guidance %q", need)
					}
				}
			})
		}
	}
}

func stringBool(v bool) string {
	if v {
		return "error"
	}
	return "no-error"
}

func TestCompletedBrowserFalseIsAnAssertionFailure(t *testing.T) {
	reg := &fakeReg{out: "browser diagnostics: {\"status\":\"completed\"}\neval result: false"}
	v := RunVerify(context.Background(), reg, "/unused", &plan.Verify{Kind: "eval", Expr: "window.ready", Path: "index.html"})
	if v.Pass || v.FailureKind != "" || browserRecoveryGuidance(v) != "" {
		t.Fatalf("a real false result was misclassified as unavailable: %+v", v)
	}
}

func TestBrowserErrorWithoutDiagnosticsRetainsOutput(t *testing.T) {
	reg := &fakeReg{out: "launch diagnostic", err: errors.New("not installed")}
	v := RunVerify(context.Background(), reg, "/unused", &plan.Verify{Kind: "eval", Expr: "true", Path: "index.html"})
	if v.Pass || v.FailureKind != "browser_unavailable" || !strings.Contains(v.Evidence, "launch diagnostic") || !strings.Contains(v.Evidence, "not installed") {
		t.Fatalf("lost launch failure evidence: %+v", v)
	}
}

type browserVerificationRunner struct{ page *tools.CheckPage }

func (r browserVerificationRunner) ExecuteMode(ctx context.Context, name string, args json.RawMessage, mode string) (string, error) {
	return r.page.Execute(ctx, args)
}

func TestBrowserVerifyAwaitsBeforeBooleanCoercion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><html><body>verify fixture</body></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := browserVerificationRunner{page: tools.NewCheckPage(dir)}
	for _, tc := range []struct {
		expr string
		pass bool
		kind string
	}{
		{"true", true, ""}, {"false", false, ""},
		{"Promise.resolve(true)", true, ""}, {"Promise.resolve(false)", false, ""},
		{"Promise.reject(new Error('rejected fixture'))", false, ""},
		{"new Promise(() => {})", false, "browser_eval_timeout"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			check := &plan.Verify{Kind: "eval", Path: "index.html", Expr: tc.expr}
			v := RunVerify(context.Background(), reg, dir, check)
			if strings.Contains(v.Evidence, "no headless browser available") {
				t.Skip("real browser verification unverified: no browser installed")
			}
			if v.Pass != tc.pass || v.FailureKind != tc.kind || check.Expr != tc.expr {
				t.Fatalf("wrong awaited verdict for %s: %+v", tc.expr, v)
			}
		})
	}
}
