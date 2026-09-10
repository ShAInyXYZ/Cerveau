package loop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cerveau/internal/plan"
)

type fakeReg struct {
	out  string
	err  error
	name string
	args json.RawMessage
}

func (f *fakeReg) ExecuteMode(_ context.Context, name string, args json.RawMessage, _ string) (string, error) {
	f.name, f.args = name, args
	return f.out, f.err
}

func TestVerifyContains(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "fan.js"), []byte("export function buildFan(){}"), 0o644)

	if v := RunVerify(context.Background(), nil, ws, &plan.Verify{Kind: "contains", File: "fan.js", Symbol: "buildFan"}); !v.Pass {
		t.Fatalf("want pass: %+v", v)
	}
	// The file exists but the step's work is not in it — the exact case disk
	// reconciliation called "done".
	v := RunVerify(context.Background(), nil, ws, &plan.Verify{Kind: "contains", File: "fan.js", Symbol: "buildCage"})
	if v.Pass {
		t.Fatalf("a file that exists but lacks the symbol must FAIL: %+v", v)
	}
	// Missing file fails closed too.
	if v := RunVerify(context.Background(), nil, ws, &plan.Verify{Kind: "contains", File: "nope.js", Symbol: "x"}); v.Pass {
		t.Fatal("missing file must fail")
	}
}

func TestVerifyEvalTruthiness(t *testing.T) {
	pass := &fakeReg{out: `eval result: true", source: file:///w/index.html (17)`}
	if v := RunVerify(context.Background(), pass, "/w", &plan.Verify{Kind: "eval", Expr: "!!x", Path: "index.html"}); !v.Pass {
		t.Fatalf("true must pass: %+v", v)
	}
	if pass.name != "check_page" {
		t.Fatalf("wrong tool: %s", pass.name)
	}
	fail := &fakeReg{out: `eval result: false", source: file:///w/index.html (17)`}
	if v := RunVerify(context.Background(), fail, "/w", &plan.Verify{Kind: "eval", Expr: "!!x", Path: "index.html"}); v.Pass {
		t.Fatalf("false must fail: %+v", v)
	}
	// No result line at all (the car run's "eval produced no result") is NOT a pass.
	none := &fakeReg{out: "eval produced no result — the expression may have thrown"}
	if v := RunVerify(context.Background(), none, "/w", &plan.Verify{Kind: "eval", Expr: "!!x", Path: "index.html"}); v.Pass {
		t.Fatalf("no result must fail closed: %+v", v)
	}
	// A tool error is not a pass either.
	broke := &fakeReg{err: errors.New("no browser")}
	if v := RunVerify(context.Background(), broke, "/w", &plan.Verify{Kind: "eval", Expr: "!!x", Path: "index.html"}); v.Pass {
		t.Fatal("tool error must fail closed")
	}
}

func TestVerifyCommandExitCode(t *testing.T) {
	ok := &fakeReg{out: "syntax OK"}
	if v := RunVerify(context.Background(), ok, "/w", &plan.Verify{Kind: "command", Command: "node --check a.js"}); !v.Pass {
		t.Fatalf("exit 0 must pass: %+v", v)
	}
	bad := &fakeReg{out: "SyntaxError", err: errors.New("exit status 1")}
	v := RunVerify(context.Background(), bad, "/w", &plan.Verify{Kind: "command", Command: "node --check a.js"})
	if v.Pass {
		t.Fatal("non-zero exit must fail")
	}
	if v.Evidence == "" {
		t.Fatal("a failure must carry its output")
	}
}

func TestVerifyEvalAcceptsOnlyExactBooleanEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		pass bool
	}{
		{"bare true", "eval result: true", true},
		{"quoted true", `eval result: "true"`, true},
		{"chromium file source", `eval result: true", source: file:///w/index.html (17)`, true},
		{"chromium http source", `eval result: true", source: http://localhost:8000/index.html (117)`, true},
		{"surrounding whitespace", " \teval result: true \r\nno console errors", true},
		{"false", `eval result: false`, false},
		{"quoted false", `eval result: "false"`, false},
		{"false with source", `eval result: false", source: file:///w/index.html (17)`, false},
		{"truthy prefix", `eval result: truegarbage`, false},
		{"truthy prefix with source", `eval result: truegarbage", source: file:///w/index.html (17)`, false},
		{"trailing prose", `eval result: true but actually false`, false},
		{"malformed source suffix", `eval result: true", source: unknown`, false},
		{"source with trailing garbage", `eval result: true", source: file:///w/index.html (17) garbage`, false},
		{"unbalanced quote", `eval result: true"`, false},
		{"unrelated console text", `console message: eval result: true`, false},
		{"unrelated console text then false", "console message: eval result: true\neval result: false", false},
		{"number is not boolean", `eval result: 1`, false},
		{"object is not boolean", `eval result: {"pass":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := &fakeReg{out: tc.out}
			v := RunVerify(context.Background(), reg, "/w", &plan.Verify{Kind: "eval", Expr: "!!x", Path: "index.html"})
			if v.Pass != tc.pass {
				t.Fatalf("Pass = %v, want %v for %q", v.Pass, tc.pass, tc.out)
			}
			var args map[string]string
			if err := json.Unmarshal(reg.args, &args); err != nil {
				t.Fatal(err)
			}
			if args["eval"] != "Promise.resolve((!!x)).then(value => JSON.stringify(!!value))" {
				t.Fatalf("verification did not request an actual boolean: %q", args["eval"])
			}
		})
	}
}

func TestVerifyNilIsNeverAPass(t *testing.T) {
	if RunVerify(context.Background(), nil, "/w", nil).Pass {
		t.Fatal("no verify must never read as done")
	}
}
