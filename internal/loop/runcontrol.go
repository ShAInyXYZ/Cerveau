package loop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cerveau/internal/episodic"
)

// ErrControlConflict means that the caller must refresh its observed run before
// issuing a new control. Retrying the same accepted control is always harmless.
var ErrControlConflict = errors.New("run control conflict")

type RunControl struct {
	RunID   string `json:"run_id"`
	ID      string `json:"control_id"`
	Version uint64 `json:"control_version"`
	Text    string `json:"text,omitempty"`
}

type controlReceipt struct {
	Kind   string   `json:"kind"`
	Action string   `json:"action"`
	ID     string   `json:"control_id"`
	Hash   string   `json:"control_hash"`
	RunID  string   `json:"run_id"`
	State  RunState `json:"state"`
}

// RunID identifies the owner carried by a tool's execution context, rather than
// whichever run happens to be current when its result reaches the UI.
func RunID(ctx context.Context) string {
	if h := handleOf(ctx); h != nil {
		return h.state.ID // immutable after admission
	}
	return ""
}

// WithRun performs a small, nonblocking delivery while the requested run is
// still the active owner. It is used for question answers; delivery cannot race
// a completed run's replacement. The callback must not call back into Loop.
func (l *Loop) WithRun(sid, runID string, deliver func() error) error {
	l.runs.mu.Lock()
	defer l.runs.mu.Unlock()
	h := l.runs.m[sid]
	if h == nil || runID == "" || h.state.ID != runID {
		return fmt.Errorf("%w: run changed or stopped", ErrControlConflict)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if terminalRun(h.state.Status) || h.killed.Load() {
		return fmt.Errorf("%w: run is stopping or finished", ErrControlConflict)
	}
	return deliver()
}

// Control applies identity validation, version validation, receipt persistence
// and the effect under the same ownership lock. A stale browser cannot affect
// a newer run, nor can a delayed pause undo a later resume of the same run.
func (l *Loop) Control(sid, action string, req RunControl) (*RunState, error) {
	if req.RunID == "" || req.ID == "" || len(req.ID) > 128 {
		return nil, fmt.Errorf("run_id and control_id required (control_id maximum 128 characters)")
	}
	switch action {
	case "pause", "resume", "kill":
		if req.Text != "" {
			return nil, fmt.Errorf("text is only valid for steer")
		}
	case "steer":
		if strings.TrimSpace(req.Text) == "" {
			return nil, fmt.Errorf("text required")
		}
	default:
		return nil, fmt.Errorf("unknown run control")
	}
	encoded, _ := json.Marshal(struct {
		Action string `json:"action"`
		RunControl
	}{action, req})
	hash := fmt.Sprintf("%x", sha256.Sum256(encoded))
	l.runs.mu.Lock()
	defer l.runs.mu.Unlock()
	h := l.runs.m[sid]
	if h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
	}

	// Persisted receipts also cover an uncertain HTTP reply after the worker
	// has finished or the service has restarted. They never restart work.
	events, err := episodic.Replay(l.path(sid))
	if err != nil {
		return nil, err
	}
	var last *RunState
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Type == episodic.RunState && last == nil {
			var st RunState
			if json.Unmarshal(ev.Payload, &st) == nil && st.ID == req.RunID {
				last = &st
			}
		}
		if ev.Type != episodic.Note {
			continue
		}
		var receipt controlReceipt
		if json.Unmarshal(ev.Payload, &receipt) != nil || receipt.Kind != "run_control" || receipt.ID != req.ID {
			continue
		}
		if receipt.Hash != hash {
			return nil, fmt.Errorf("%w: control_id already used for a different request", ErrControlConflict)
		}
		if h != nil && h.state.ID == req.RunID {
			st := h.state
			return &st, nil
		}
		if last == nil || last.ControlVersion < receipt.State.ControlVersion {
			last = &receipt.State
		}
		if !terminalRun(last.Status) {
			last.Status, last.Reason = "interrupted", "worker stopped; the accepted control will not be replayed"
		}
		return last, nil
	}
	if h == nil || h.state.ID != req.RunID || terminalRun(h.state.Status) || h.killed.Load() {
		return nil, fmt.Errorf("%w: run changed or stopped", ErrControlConflict)
	}
	if req.Version != h.state.ControlVersion {
		return nil, fmt.Errorf("%w: controls changed; refresh the run", ErrControlConflict)
	}
	if h.writer == nil {
		return nil, fmt.Errorf("run is not ready to accept controls")
	}
	if err := h.writer.Error(); err != nil {
		return nil, fmt.Errorf("event persistence failed: %w", err)
	}
	if action == "resume" && !h.paused.Load() {
		return nil, fmt.Errorf("%w: run is not paused", ErrControlConflict)
	}
	if action == "steer" {
		if _, err := h.writer.Append(episodic.MsgUser, map[string]any{
			"text": req.Text, "steer": true, "control_id": req.ID,
		}); err != nil {
			return nil, err
		}
	}
	next := h.state
	next.ControlVersion++
	if _, err := h.writer.Append(episodic.Note, controlReceipt{
		Kind: "run_control", Action: action, ID: req.ID, Hash: hash, RunID: req.RunID, State: next,
	}); err != nil {
		return nil, err
	}
	h.state.ControlVersion = next.ControlVersion
	status, phase, tool, reason := h.state.Status, h.state.Phase, h.state.Tool, h.state.Reason
	switch action {
	case "pause":
		h.paused.Store(true)
		status, reason = "pause_requested", "waiting for a safe boundary"
	case "resume":
		h.paused.Store(false)
		status, reason = "running", "resumed"
	case "kill":
		h.killed.Store(true)
		status, reason = "cancelling", "stopping active operations; completed files are retained"
	case "steer":
		h.steered.Store(true)
		reason = "steering accepted; applied at the next model boundary"
	}
	if err := h.publishLocked(status, phase, tool, reason); err != nil {
		return nil, err
	}
	if h.inFlight != nil && (action == "kill" || action == "pause" || action == "steer" && phase == "model_call") {
		h.inFlight()
	}
	if action == "kill" && h.rootCancel != nil {
		h.rootCancel()
	}
	if action == "resume" {
		select {
		case h.wake <- struct{}{}:
		default:
		}
	}
	st := h.state
	return &st, nil
}
