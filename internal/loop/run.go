package loop

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/tools"
)

var ErrBusy = errors.New("run already active")

type RunState struct {
	Result         *Result   `json:"result,omitempty"`
	ID             string    `json:"id"`
	Status         string    `json:"status"`
	Phase          string    `json:"phase,omitempty"`
	Tool           string    `json:"tool,omitempty"`
	Step           int       `json:"step"`
	Started        time.Time `json:"started"`
	Updated        time.Time `json:"updated"`
	Reason         string    `json:"reason,omitempty"`
	Workspace      string    `json:"workspace"`
	ThinkingMode   string    `json:"thinking_mode"`
	ThinkingEffort string    `json:"thinking_effort"`
	Sampling       string    `json:"sampling"`
	CommandID      string    `json:"command_id,omitempty"`
	CommandHash    string    `json:"command_hash,omitempty"`
	Calls          int       `json:"calls"`
	ControlVersion uint64    `json:"control_version"`
}

type runHandle struct {
	rootCancel context.CancelFunc

	mu       sync.Mutex
	inFlight context.CancelFunc
	paused   atomic.Bool
	killed   atomic.Bool
	steered  atomic.Bool // set ONLY by a real user steer, so an incidental
	// context cancel (e.g. a flaky model endpoint) is not
	// mistaken for one and silently swallowed
	state      RunState
	writer     *episodic.Writer
	wake       chan struct{}
	registry   *tools.Registry
	skillNotes []string
	brief      string
	verifySeq  atomic.Int64
}

type runKey struct{}

func handleOf(ctx context.Context) *runHandle { h, _ := ctx.Value(runKey{}).(*runHandle); return h }

func (h *runHandle) publish(status, phase, tool, reason string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.publishLocked(status, phase, tool, reason)
}

func terminalRun(status string) bool {
	return status == "completed" || status == "cancelled" || status == "interrupted" || status == "failed" || status == "suspended"
}

func (h *runHandle) publishLocked(status, phase, tool, reason string) error {
	// A concurrently finishing tool/question must not undo an acknowledged
	// control or resurrect a terminal run while its owner is being released.
	if terminalRun(h.state.Status) && !terminalRun(status) {
		return nil
	}
	if !terminalRun(status) {
		if h.killed.Load() {
			status, reason = "cancelling", "stopping active operations; completed files are retained"
		} else if h.paused.Load() && status != "paused" {
			status, reason = "pause_requested", "waiting for a safe boundary"
		}
	}
	h.state.Status, h.state.Phase, h.state.Tool, h.state.Reason = status, phase, tool, reason
	h.state.Updated = time.Now().UTC()
	if h.writer == nil {
		return nil
	}
	_, err := h.writer.Append(episodic.RunState, h.state)
	return err
}

func (h *runHandle) boundary(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.writer != nil {
		if err := h.writer.Error(); err != nil {
			return fmt.Errorf("event persistence failed: %w", err)
		}
	}
	if !h.paused.Load() {
		return nil
	}
	if err := h.publish("paused", "", "", "paused at a safe boundary"); err != nil {
		return err
	}
	for h.paused.Load() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-h.wake:
		}
	}
	return h.publish("running", "", "", "resumed")
}

// beginRun reserves before any prompt, tool or user event. Nested plan handoffs
// carry the SAME owner, cancellation context and scoped capability bundle.
func (l *Loop) beginRun(ctx context.Context, sid, mode, brief string) (context.Context, *runHandle, func(*Result, error), error) {
	if h := handleOf(ctx); h != nil {
		return ctx, h, func(*Result, error) {}, nil
	}
	ws := ""
	if l.workspace != nil {
		ws = l.workspace(sid)
	}
	if ws != "" {
		ws, _ = filepath.Abs(ws)
		if resolved, err := filepath.EvalSymlinks(ws); err == nil {
			ws = resolved
		}
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return ctx, nil, nil, err
	}
	root, cancel := context.WithCancel(tools.WithSession(ctx, sid))
	h := &runHandle{rootCancel: cancel, wake: make(chan struct{}, 1), brief: brief,
		state: RunState{ID: hex.EncodeToString(idBytes), Status: "running", Step: -1, Started: time.Now().UTC(), Workspace: ws}}
	h.state.ThinkingMode, h.state.ThinkingEffort = l.Thinking()
	h.state.Sampling = samplingOf(ctx)
	if h.state.Sampling == "" {
		h.state.Sampling = l.SamplingName()
	}
	root = WithSampling(root, h.state.Sampling)
	if cmd, ok := ctx.Value(commandKey{}).(commandIdentity); ok {
		h.state.CommandID, h.state.CommandHash = cmd.ID, cmd.Hash
	}
	l.runs.mu.Lock()
	for owner, current := range l.runs.m {
		if owner == sid || (ws != "" && current.state.Workspace == ws) {
			l.runs.mu.Unlock()
			cancel()
			return ctx, nil, nil, fmt.Errorf("%w: session %s owns the session/workspace", ErrBusy, owner)
		}
	}
	l.runs.m[sid] = h
	l.runs.mu.Unlock()
	var locks []*os.File
	release := func() {
		cancel()
		for _, f := range locks {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}
		l.runs.mu.Lock()
		if l.runs.m[sid] == h {
			delete(l.runs.m, sid)
		}
		l.runs.mu.Unlock()
	}
	// Cross-process ownership, without modifying user workspaces.
	lockPaths := []string{l.path(sid) + ".run.lock"}
	if ws != "" {
		lockPaths = append(lockPaths, filepath.Join(os.TempDir(), fmt.Sprintf("cerveau-workspace-%x.lock", sha256.Sum256([]byte(ws)))))
	}
	for _, path := range lockPaths {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			release()
			return ctx, nil, nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			release()
			return ctx, nil, nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			release()
			return ctx, nil, nil, fmt.Errorf("%w: another process owns this workspace/session", ErrBusy)
		}
		locks = append(locks, f)
	}
	wr, err := l.open(sid)
	if err != nil {
		release()
		return ctx, nil, nil, err
	}
	// A previous owner vanished without a terminal event. Recover its state
	// before accepting a new owner; uncertain effects are never labelled passed.
	prior, _ := episodic.Replay(l.path(sid))
	for i := len(prior) - 1; i >= 0; i-- {
		if prior[i].Type == episodic.RunState {
			var st RunState
			if json.Unmarshal(prior[i].Payload, &st) == nil && (st.Status == "running" || st.Status == "paused" || st.Status == "pause_requested" || st.Status == "waiting_user" || st.Status == "cancelling") {
				st.Status = "interrupted"
				st.Reason = "previous worker stopped; inspect unfinished tool effects"
				st.Updated = time.Now().UTC()
				if _, err := wr.Scoped(map[string]any{"run_id": st.ID, "session_id": sid}).Append(episodic.RunState, st); err != nil {
					release()
					return ctx, nil, nil, err
				}
			}
			break
		}
	}
	h.mu.Lock()
	h.writer = wr.Scoped(map[string]any{"run_id": h.state.ID, "session_id": sid, "schema_version": 2})
	h.mu.Unlock()
	if err := h.publish("running", "accepted", "", ""); err != nil {
		release()
		return ctx, nil, nil, err
	}
	root = context.WithValue(root, runKey{}, h)
	finish := func(res *Result, runErr error) {
		h.mu.Lock()
		status, reason := "completed", ""
		if res != nil {
			reason = res.StopReason
			res.RunID = h.state.ID
		}
		switch {
		case h.killed.Load():
			status, reason = "cancelled", "cancelled by user"
		case root.Err() != nil:
			status, reason = "interrupted", root.Err().Error()
		case runErr != nil:
			status, reason = "failed", runErr.Error()
		case res != nil && res.StopReason != "final_answer" && res.StopReason != "":
			status = "suspended"
		}
		h.state.Result = res
		_ = h.publishLocked(status, "", "", reason)
		h.mu.Unlock()
		release()
	}
	return root, h, finish, nil
}

func (l *Loop) RunStateOf(sid string) *RunState {
	if h := l.runs.get(sid); h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		st := h.state
		return &st
	}
	projection, err := l.Snapshot(sid)
	if err != nil {
		return nil
	}
	return projection.Run
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
		if r.m[sessionID] == h {
			delete(r.m, sessionID)
		}
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
		h.mu.Lock()
		model := h.state.Phase == "model_call"
		h.mu.Unlock()
		if model {
			h.cancelInFlight()
		}
		return true
	}
	return false
}

func (l *Loop) Pause(sessionID string) bool {
	if h := l.runs.get(sessionID); h != nil {
		h.paused.Store(true)
		_ = h.publish("pause_requested", "", "", "waiting for a safe boundary")
		h.cancelInFlight()
		return true
	}
	return false
}

func (l *Loop) Resume(sessionID string) bool {
	if h := l.runs.get(sessionID); h != nil && h.paused.Swap(false) {
		select {
		case h.wake <- struct{}{}:
		default:
		}
		return true
	}
	return false
}

func (l *Loop) Kill(sessionID string) bool {
	if h := l.runs.get(sessionID); h != nil {
		h.killed.Store(true)
		_ = h.publish("cancelling", "", "", "stopping active operations; completed files are retained")
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

func (h *runHandle) thinkingFor(mode string, planning bool) string {
	policy, effort := h.state.ThinkingMode, h.state.ThinkingEffort
	if policy == "always" || policy == "autopilot" && mode == "autopilot" || planning && policy == "plan" && mode == "autopilot" {
		return effort
	}
	return llm.ThinkingOff
}

// Waiting retains ownership until an answer or cancellation.
func SetWaiting(ctx context.Context, waiting bool) {
	if h := handleOf(ctx); h != nil {
		if waiting {
			_ = h.publish("waiting_user", "ask_user", "", "")
		} else {
			_ = h.publish("running", "", "", "")
		}
	}
}
