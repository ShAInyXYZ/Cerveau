// Package idle parks the Brain Core when nobody is using it.
//
// A Core holds its weights and KV pool for as long as it runs, whether or not
// anyone is talking to it. Left overnight that is a night of power spent on an
// empty room — the machine is not thinking, it is only warm.
//
// This package decides WHEN to park. It never parks anything itself.
//
// That split is deliberate, and it is the same invariant cores.go states: a
// harness that can stop the engine it is talking to has a failure mode where
// the machine ends up with no model at all and no way to say so. So Cerveau
// writes a REQUEST — a small file — and a systemd watchdog outside this process
// is what actually stops the unit. If Cerveau dies mid-transition, systemd
// still holds both ends, and socket activation brings the Core back on the next
// real request. Nothing here can orphan the machine.
package idle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is what the panel renders.
type State string

const (
	// Active — the Core is loaded and the timer is running (or held).
	Active State = "active"
	// Warning — the park is close enough to tell the user about, and early
	// enough that they can still wave it off.
	Warning State = "warning"
	// Parked — the Core is unloaded. The next turn pays a cold start.
	Parked State = "parked"
	// Waking — a request arrived while parked; the Core is loading.
	Waking      State = "waking"
	Unavailable State = "unavailable"
)

// Config carries the two durations that shape the feature.
type Config struct {
	// After is the silence required before parking.
	After time.Duration
	// Warn is how long BEFORE parking the user is told. The whole point of
	// the warning is that it is actionable, so this is not a toast at the
	// moment of parking — it is a window with time left in it.
	Warn time.Duration
	// Enabled is the master switch; false means the Core never parks.
	Enabled bool
}

// DefaultConfig reflects the cost of a cold start on this machine: a local
// reload is on the order of a minute or two, which is cheap enough that a long
// TTL buys little. An hour of true silence is a good sign the room is empty.
//
// The warning lands at 45 minutes — a quarter of an hour of standing notice,
// which is enough time to notice it, decide, and act without being rushed.
func DefaultConfig() Config {
	return Config{After: time.Hour, Warn: 15 * time.Minute, Enabled: true}
}

// Tracker follows activity and reports which State the Core is in.
//
// It counts SILENCE, not wall-clock time since the last chat message: a
// workflow running unattended is exactly the case the user said must survive,
// so any in-flight work HOLDS the timer rather than merely resetting it.
type Tracker struct {
	mu   sync.Mutex
	cfg  Config
	last time.Time // last observed activity
	// holds counts work in flight. While > 0 the timer does not advance at
	// all — a long generation must not be parked out from under itself.
	holds int
	// parked is set by the watchdog acknowledging the request, not by us
	// guessing. Reality comes from whether the endpoint answers.
	parked      bool
	waking      bool
	unavailable bool
	// snoozedUntil suppresses the warning after the user waves it off.
	snoozedUntil time.Time
	// requestPath is the file the systemd watchdog polls.
	requestPath string
}

func New(cfg Config, requestPath string) *Tracker {
	return &Tracker{cfg: cfg, last: time.Now(), requestPath: requestPath}
}

// Touch records activity. Cheap enough to call on every request.
func (t *Tracker) Touch() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = time.Now()
	t.snoozedUntil = time.Time{}
}

// Hold marks work in flight and returns the release. While any hold is
// outstanding the idle clock does not advance.
//
// Returning a closure rather than exposing Release keeps the pairing local to
// the caller — the release cannot be forgotten in one branch of a switch.
func (t *Tracker) Hold() func() {
	t.mu.Lock()
	t.holds++
	t.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			if t.holds > 0 {
				t.holds--
			}
			t.last = time.Now()
			t.mu.Unlock()
		})
	}
}

// Snooze pushes the park out by d, so a user who is still at the desk can
// dismiss the warning without disabling the feature.
func (t *Tracker) Snooze(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = time.Now()
	t.snoozedUntil = time.Now().Add(d)
}

// SetParked records what the watchdog actually did. State follows reality.
func (t *Tracker) SetParked(v bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.parked = v
	if !v {
		t.last = time.Now()
		t.waking = false
	}
}

// SetWaking marks a cold start in progress, so the panel can explain the wait
// rather than looking hung.
func (t *Tracker) SetWaking(v bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.waking = v
}

// Observe reconciles service reality without touching the Core or resetting
// the idle clock on every successful poll. Empty means observation failed.
func (t *Tracker) Observe(state State) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch state {
	case Parked, Waking, Unavailable, Active:
		if state == Active && (t.parked || t.waking || t.unavailable) {
			t.last = time.Now()
		}
		t.parked, t.waking, t.unavailable = state == Parked, state == Waking, state == Unavailable
	}
}

// Status is the panel's view of the world.
type Status struct {
	State State `json:"state"`
	// IdleFor is how long the Core has been silent.
	IdleSeconds int `json:"idle_seconds"`
	// ParkInSeconds counts down to the park. Negative means overdue (the
	// watchdog has been asked and has not yet acted).
	ParkInSeconds int  `json:"park_in_seconds"`
	Enabled       bool `json:"enabled"`
	Held          bool `json:"held"`
	AfterSeconds  int  `json:"after_seconds"`
	WarnSeconds   int  `json:"warn_seconds"`
}

// Status computes the current state. Pure read — parking is requested by Tick.
func (t *Tracker) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status()
}

func (t *Tracker) status() Status {
	s := Status{
		Enabled:      t.cfg.Enabled,
		Held:         t.holds > 0,
		AfterSeconds: int(t.cfg.After.Seconds()),
		WarnSeconds:  int(t.cfg.Warn.Seconds()),
	}
	switch {
	case t.unavailable:
		s.State = Unavailable
	case t.waking:
		s.State = Waking
	case t.parked:
		s.State = Parked
	default:
		s.State = Active
	}

	idle := time.Since(t.last)
	s.IdleSeconds = int(idle.Seconds())

	if !t.cfg.Enabled || t.holds > 0 || t.parked || t.waking || t.unavailable {
		// Nothing is counting down, so a countdown would be a lie.
		s.ParkInSeconds = -1
		return s
	}

	remaining := t.cfg.After - idle
	s.ParkInSeconds = int(remaining.Seconds())
	if remaining <= t.cfg.Warn && time.Now().After(t.snoozedUntil) {
		s.State = Warning
	}
	return s
}

// Tick advances the state machine and writes the park request when due.
// Returns true if it asked for a park on this call.
func (t *Tracker) Tick() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.cfg.Enabled || t.parked || t.waking || t.unavailable || t.holds > 0 {
		return false
	}
	if time.Since(t.last) < t.cfg.After {
		return false
	}
	// Silence long enough, nothing in flight: ask the watchdog to park.
	// We do NOT set parked here — that is the watchdog's to confirm, and
	// claiming it early would show "parked" over a Core still serving.
	if err := t.writeRequest(); err != nil {
		return false
	}
	return true
}

// writeRequest drops the file the systemd watchdog polls. Written atomically so
// the watchdog never reads a half-written request.
func (t *Tracker) writeRequest() error {
	if t.requestPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(t.requestPath), 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"requested_at": time.Now().UTC().Format(time.RFC3339),
		"reason":       "idle",
		"idle_seconds": int(time.Since(t.last).Seconds()),
	})
	if err != nil {
		return err
	}
	tmp := t.requestPath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, t.requestPath)
}

// ClearRequest removes a pending park request — used when the user waves the
// warning off after the request was already written.
func (t *Tracker) ClearRequest() {
	if t.requestPath != "" {
		_ = os.Remove(t.requestPath)
	}
}

// ParkNow requests an immediate park, for the "yes, go idle" button.
func (t *Tracker) ParkNow() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writeRequest()
}

// SetConfig updates the durations live, so the panel's settings take effect
// without a restart.
func (t *Tracker) SetConfig(c Config) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cfg = c
	t.last = time.Now()
}

// Cfg returns the current config.
func (t *Tracker) Cfg() Config {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cfg
}

// DefaultRequestPath is where the park request is written for the systemd
// watchdog to find.
//
// XDG_RUNTIME_DIR is tmpfs, which is the right home for it: a park request is
// only meaningful to the running system, and a stale one surviving a reboot
// would park a Core the user just started.
func DefaultRequestPath() string {
	if run := os.Getenv("XDG_RUNTIME_DIR"); run != "" {
		return filepath.Join(run, "cerveau", "park-request.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".crv", "run", "park-request.json")
}
