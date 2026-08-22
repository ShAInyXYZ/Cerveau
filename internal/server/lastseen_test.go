package server

import (
	"testing"
	"time"
)

// "Is this device still in use?" was unanswerable: the list showed when a
// device was ADDED and nothing since. A phone paired in March and a phone used
// this morning looked identical, so revoking safely meant guessing.
func TestTouchRecordsLastSeen(t *testing.T) {
	withTempHome(t)
	if err := registerDevice("dev-a", "K1"); err != nil {
		t.Fatal(err)
	}
	if d := findDevice("dev-a"); d == nil || d.LastSeen != "" {
		t.Fatal("a freshly paired device should have no last-seen yet")
	}

	touchDevice("dev-a")

	d := findDevice("dev-a")
	if d == nil || d.LastSeen == "" {
		t.Fatal("touchDevice did not record anything")
	}
	if _, err := time.Parse(time.RFC3339, d.LastSeen); err != nil {
		t.Errorf("last_seen is not RFC3339: %q", d.LastSeen)
	}
}

// Every authenticated request touches the device, so the write must not run
// on each one — that would rewrite devices.json continuously under load.
func TestTouchIsThrottled(t *testing.T) {
	withTempHome(t)
	registerDevice("dev-b", "K1")

	touchDevice("dev-b")
	first := findDevice("dev-b").LastSeen

	touchDevice("dev-b") // immediately again
	if got := findDevice("dev-b").LastSeen; got != first {
		t.Errorf("a second touch inside the window rewrote the file: %q -> %q", first, got)
	}
}

// An unknown id must not create a phantom entry.
func TestTouchIgnoresUnknownDevice(t *testing.T) {
	withTempHome(t)
	touchDevice("nobody")
	if len(loadDevices()) != 0 {
		t.Error("touching an unknown id created a device")
	}
}
