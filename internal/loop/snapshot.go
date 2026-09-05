package loop

import (
	"cerveau/internal/episodic"
	"encoding/json"
	"os"
	"syscall"
)

// Projection is reduced from one durable journal prefix. Observers must not
// combine a plan from one replay with a run from a later replay.
type Projection struct {
	Events  []episodic.Event `json:"events"`
	Cursor  string           `json:"cursor"`
	Run     *RunState        `json:"run"`
	Plan    *PlanState       `json:"plan_state"`
	Report  *Report          `json:"report"`
	Running bool             `json:"running"`
}

func ActiveRunStatus(status string) bool {
	switch status {
	case "running", "paused", "pause_requested", "waiting_user", "cancelling":
		return true
	}
	return false
}

func ProjectEvents(events []episodic.Event, ownerID string) *Projection {
	p := &Projection{Events: events}
	if len(events) > 0 {
		p.Cursor = events[len(events)-1].ID
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != episodic.RunState {
			continue
		}
		var run RunState
		if json.Unmarshal(events[i].Payload, &run) == nil {
			if ActiveRunStatus(run.Status) && run.ID != ownerID {
				run.Status = "interrupted"
				run.Reason = "worker stopped before recording completion; inspect unfinished tool effects before retrying"
			}
			p.Run = &run
			p.Running = ActiveRunStatus(run.Status)
		}
		break
	}
	s, id, err := ReducePlan(events)
	if err == nil {
		if p.Run != nil && p.Run.Status == "interrupted" {
			for i := range s.Steps {
				if s.Steps[i].Status == "running" || s.Steps[i].Status == "verifying" {
					s.Steps[i].Status = "pending"
					s.Steps[i].Reason = p.Run.Reason
				}
			}
		}
		p.Plan = supervisorState(s, id)
		p.Report = reportFromSupervisor(s, id)
	}
	return p
}

func (l *Loop) Snapshot(sid string) (*Projection, error) {
	// Registration/release cannot race the owner check. All published data comes
	// from the log, never the mutable handle's newer phase/result.
	l.runs.mu.Lock()
	defer l.runs.mu.Unlock()
	events, err := episodic.Replay(l.path(sid))
	if err != nil {
		return nil, err
	}
	ownerID := ""
	if h := l.runs.m[sid]; h != nil {
		ownerID = h.state.ID
	} else if externalRunOwner(l.path(sid) + ".run.lock") {
		// A CLI/second process may own the lease. Absence from this process's
		// handle registry alone is not evidence of a crash.
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Type == episodic.RunState {
				var st RunState
				_ = json.Unmarshal(events[i].Payload, &st)
				ownerID = st.ID
				break
			}
		}
	}
	return ProjectEvents(events, ownerID), nil
}

func externalRunOwner(path string) bool {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer file.Close()
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return false
	}
	return err == syscall.EWOULDBLOCK || err == syscall.EAGAIN
}
