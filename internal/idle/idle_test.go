package idle

import (
	"path/filepath"
	"testing"
	"time"
)

func cfg(after, warn time.Duration) Config {
	return Config{After: after, Warn: warn, Enabled: true}
}

func TestWarningAppearsBeforePark(t *testing.T) {
	tr := New(cfg(time.Hour, 15*time.Minute), "")
	// 50 minutes of silence: inside the 15-minute warning window.
	tr.last = time.Now().Add(-50 * time.Minute)

	s := tr.Status()
	if s.State != Warning {
		t.Fatalf("state = %q, want %q", s.State, Warning)
	}
	if s.ParkInSeconds <= 0 || s.ParkInSeconds > 600 {
		t.Fatalf("park_in = %ds, want ~600", s.ParkInSeconds)
	}
}

func TestHoldStopsTheClock(t *testing.T) {
	tr := New(cfg(time.Minute, 30*time.Second), "")
	release := tr.Hold()
	tr.last = time.Now().Add(-time.Hour) // long past due

	if s := tr.Status(); s.State != Active || !s.Held {
		t.Fatalf("held work should stay active, got %q held=%v", s.State, s.Held)
	}
	if tr.Tick() {
		t.Fatal("parked while work was in flight")
	}
	release()
	if s := tr.Status(); s.Held {
		t.Fatal("hold still reported after release")
	}
}

func TestTickWritesRequestWhenDue(t *testing.T) {
	p := filepath.Join(t.TempDir(), "park.json")
	tr := New(cfg(time.Minute, 30*time.Second), p)
	tr.last = time.Now().Add(-2 * time.Minute)

	if !tr.Tick() {
		t.Fatal("Tick did not request a park when overdue")
	}
	// The request is a signal to the watchdog; state must NOT flip to parked
	// on our say-so, because the Core is still serving until systemd acts.
	if s := tr.Status(); s.State == Parked {
		t.Fatal("state claimed parked before the watchdog confirmed")
	}
}

func TestSnoozeSuppressesWarning(t *testing.T) {
	tr := New(cfg(time.Hour, 15*time.Minute), "")
	tr.last = time.Now().Add(-50 * time.Minute)
	if tr.Status().State != Warning {
		t.Fatal("precondition: expected warning")
	}
	tr.Snooze(30 * time.Minute)
	if s := tr.Status(); s.State != Active {
		t.Fatalf("after snooze state = %q, want active", s.State)
	}
}

func TestDisabledNeverParks(t *testing.T) {
	tr := New(Config{After: time.Minute, Warn: time.Second, Enabled: false}, "")
	tr.last = time.Now().Add(-time.Hour)
	if tr.Tick() {
		t.Fatal("parked while disabled")
	}
	if s := tr.Status(); s.State != Active {
		t.Fatalf("state = %q, want active", s.State)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	tr := New(cfg(time.Minute, time.Second), "")
	r := tr.Hold()
	r()
	r() // a double release must not underflow into a negative hold count
	if s := tr.Status(); s.Held {
		t.Fatal("hold count went wrong on double release")
	}
}

func TestWakingOutranksParked(t *testing.T) {
	tr := New(cfg(time.Minute, time.Second), "")
	tr.SetParked(true)
	tr.SetWaking(true)
	if s := tr.Status(); s.State != Waking {
		t.Fatalf("state = %q, want %q", s.State, Waking)
	}
}
