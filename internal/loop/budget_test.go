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

// The time guard must measure STUCKNESS, not duration. A turn that keeps
// making progress runs as long as it needs; only a turn that stops
// progressing trips the clock. A slow local model doing real work was
// being killed identically to one spinning in a loop.
func TestTimeGuardResetsOnProgress(t *testing.T) {
	g := newTurnGuardBudget(0, 60*time.Millisecond)

	// No progress: the idle clock runs out.
	time.Sleep(80 * time.Millisecond)
	if _, _, tripped := g.preThink(1); !tripped {
		t.Fatal("idle turn should trip the time guard")
	}

	// With progress, the same elapsed time must NOT trip it.
	g2 := newTurnGuardBudget(0, 60*time.Millisecond)
	for i := 0; i < 4; i++ {
		time.Sleep(30 * time.Millisecond)
		g2.progress() // a tool ran, a file was written — real work
		if _, _, tripped := g2.preThink(i + 1); tripped {
			t.Fatalf("progress at %d should have reset the idle clock", i)
		}
	}

	// Stop progressing and it trips again.
	time.Sleep(80 * time.Millisecond)
	if _, _, tripped := g2.preThink(9); !tripped {
		t.Fatal("turn that stopped progressing should trip")
	}
}
