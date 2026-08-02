package loop

import (
	"context"
	"testing"
	"time"
)

// A supervised plan step gets the build-sized budget; a normal chat turn
// keeps the conversational one.
func TestTurnBudgetPerContext(t *testing.T) {
	if got := turnBudget(context.Background()); got != maxTurnTime {
		t.Fatalf("plain turn budget = %s, want %s", got, maxTurnTime)
	}
	if got := turnBudget(WithLongTurn(context.Background())); got != maxStepTime {
		t.Fatalf("step budget = %s, want %s", got, maxStepTime)
	}
}

// The guard's message must report the budget it actually ran on.
func TestGuardReportsItsOwnBudget(t *testing.T) {
	g := newTurnGuardBudget(0, 50*time.Millisecond)
	time.Sleep(70 * time.Millisecond)
	stop, detail, tripped := g.preThink(1)
	if !tripped || stop != StopTime {
		t.Fatalf("expected time stop, got %q %q %v", stop, detail, tripped)
	}
	if want := "50ms"; !contains(detail, want) {
		t.Fatalf("detail %q should name the real budget %s", detail, want)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (func() bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
})() }
