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
		if _, tripped := b.record(c, "some error"); tripped {
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
