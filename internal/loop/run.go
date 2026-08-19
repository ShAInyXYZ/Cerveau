package loop

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
)

type runHandle struct {
	rootCancel context.CancelFunc

	mu       sync.Mutex
	inFlight context.CancelFunc
	paused   atomic.Bool
	killed   atomic.Bool
	steered  atomic.Bool // set ONLY by a real user steer, so an incidental
	// context cancel (e.g. a flaky model endpoint) is not
	// mistaken for one and silently swallowed
}

func (h *runHandle) setInFlight(cancel context.CancelFunc) {
	h.mu.Lock()
	h.inFlight = cancel
	h.mu.Unlock()
}

func (h *runHandle) cancelInFlight() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inFlight != nil {
		h.inFlight()
	}
}

type runsRegistry struct {
	mu sync.Mutex
	m  map[string]*runHandle
}

func newRunsRegistry() *runsRegistry {
	return &runsRegistry{m: map[string]*runHandle{}}
}

func (r *runsRegistry) register(sessionID string, h *runHandle) func() {
	r.mu.Lock()
	r.m[sessionID] = h
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.m, sessionID)
		r.mu.Unlock()
	}
}

func (r *runsRegistry) get(sessionID string) *runHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[sessionID]
}

func (l *Loop) Steer(sessionID string) bool {
	if h := l.runs.get(sessionID); h != nil {
		h.steered.Store(true) // mark BEFORE cancelling so the loop reads it as a real steer
		h.cancelInFlight()
		return true
	}
	return false
}

func (l *Loop) Pause(sessionID string) bool {
	if h := l.runs.get(sessionID); h != nil {
		h.paused.Store(true)
		return true
	}
	return false
}

func (l *Loop) Kill(sessionID string) bool {
	if h := l.runs.get(sessionID); h != nil {
		h.killed.Store(true)
		h.cancelInFlight()
		if h.rootCancel != nil {
			h.rootCancel()
		}
		return true
	}
	return false
}

// RunningSessions lists the sessions with a turn executing right now.
//
// The registry has always known this — Steer, Pause and Kill all read it — but
// nothing exposed it, so the panel could only know about turns IT started. A
// build launched from the CLI looked identical to an idle session: the user
// watching the WebUI saw nothing happening while the machine worked for half
// an hour.
func (l *Loop) RunningSessions() []string {
	l.runs.mu.Lock()
	defer l.runs.mu.Unlock()
	out := make([]string, 0, len(l.runs.m))
	for id := range l.runs.m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
