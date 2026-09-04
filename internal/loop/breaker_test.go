package loop

import (
	"strings"
	"testing"
)

// The v10 fan run made 43 bash calls and one write. 26 of them hunted for
// playwright/puppeteer — a dependency Cerveau does not have and never had.
// Each failure looked fresh to the harness, so nothing ever said "stop, the
// thing you are reaching for is not here."
//
// The breaker watches for failures of the same SHAPE. Three of those and the
// tool stops answering with an error and starts answering with a question.
func TestBreakerTripsOnRepeatedSameShapeFailure(t *testing.T) {
	b := newBashBreaker()
	cmds := []string{
		`node -e "require.resolve('playwright')"`,
		`node -e "require.resolve('playwright-core')"`,
		`node --experimental-x -e "require('playwright')"`,
	}
	var hint string
	for i, c := range cmds {
		h, tripped := b.record(c, "Error: Cannot find module 'playwright'")
		if i < 2 && tripped {
			t.Fatalf("tripped too early, at call %d", i+1)
		}
		if i == 2 {
			if !tripped {
				t.Fatal("three same-shape failures did not trip the breaker")
			}
			hint = h
		}
	}
	low := strings.ToLower(hint)
	if !strings.Contains(low, "installed") && !strings.Contains(low, "available") {
		t.Errorf("hint does not make it ask whether the thing exists:\n%s", hint)
	}
	if !strings.Contains(low, "another way") && !strings.Contains(low, "different") {
		t.Errorf("hint does not push toward an alternative approach:\n%s", hint)
	}
}

// Different work must not trip it. A model doing varied things that happen to
// fail is not stuck — it is working.
func TestBreakerIgnoresUnrelatedFailures(t *testing.T) {
	b := newBashBreaker()
	for _, c := range []string{"npm install", "python3 build.py", "go test ./...", "ls /nope"} {
		if _, tripped := b.record(c, "error: "+c); tripped {
			t.Fatalf("tripped on unrelated commands at %q", c)
		}
	}
}

// A success on that shape clears it: the model solved it and moved on.
func TestBreakerResetsAfterSuccess(t *testing.T) {
	b := newBashBreaker()
	b.record(`node -e "require('playwright')"`, "Cannot find module")
	b.record(`node -e "require('playwright')"`, "Cannot find module")
	b.ok(`node -e "require('playwright')"`)
	if _, tripped := b.record(`node -e "require('playwright')"`, "Cannot find module"); tripped {
		t.Error("tripped despite an intervening success — that is a working loop, not a stuck one")
	}
}

// The 2026-09-04 loop: four differently phrased commands, one identical
// failure. None matched a known pattern and the first word varied, so the
// old breaker counted nothing. Same error line = same wall.
func TestBreakerTripsOnSameErrorLineUnderDifferentCommands(t *testing.T) {
	b := newBashBreaker()
	cmds := []string{
		"node fix.js .crv-eval-3314114930.html",
		"bash -c 'sed -i s/x/y/ .crv-eval-2207781145.html'",
		"python3 patch.py .crv-eval-998.html",
	}
	for i, c := range cmds {
		h, tripped := b.record(c, "temp file gone\n\nexit: exit status 2")
		if i < 2 && tripped {
			t.Fatalf("tripped early at %d", i)
		}
		if i == 2 && !tripped {
			t.Fatal("three identical failures under different commands should trip")
		}
		if i == 2 && !strings.Contains(h, "temp file gone") {
			t.Fatalf("hint should name the failure, got %q", h)
		}
	}
}

// node prints its version as the last line of every uncaught exception. The
// fingerprint must be the error, not the banner — or every node failure is
// "the same wall" and a debugging session ends at four.
func TestErrorLineSkipsRuntimeTrailers(t *testing.T) {
	node := func(err string) string {
		return "/tmp/t.js:12\n    throw new Error(x);\n    ^\n\n" + err + "\n    at play (/tmp/t.js:12:11)\n    at Object.<anonymous> (/tmp/t.js:40:1)\n    at node:internal/main/run_main_module:36:49\n\nNode.js v22.23.2\n\nexit: exit status 1"
	}
	a := errorLine(node("Error: illegal move g1-f3"))
	b := errorLine(node("ReferenceError: Chess is not defined"))
	if a == b {
		t.Fatalf("two different node errors fingerprinted the same: %q", a)
	}
	if !strings.Contains(a, "illegal move") || strings.Contains(a, "Node.js") {
		t.Fatalf("fingerprint should be the error line, got %q", a)
	}
	// the same error twice is still the same wall
	if errorLine(node("Error: illegal move g1-f3")) != a {
		t.Fatal("identical errors must fingerprint identically")
	}
	// plain outputs without an error word still get a line
	if errorLine("temp file gone\n\nexit: exit status 2") != "temp file gone" {
		t.Fatalf("plain failure should be its last real line, got %q", errorLine("temp file gone\n\nexit: exit status 2"))
	}
}
