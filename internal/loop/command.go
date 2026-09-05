package loop

import (
	"cerveau/internal/episodic"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Command struct {
	ID       string `json:"command_id"`
	Kind     string `json:"kind"`
	Text     string `json:"text,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Sampling string `json:"sampling,omitempty"`
	Step     int    `json:"step"`
	Steps    []int  `json:"steps,omitempty"`
	Revision bool   `json:"revision,omitempty"`
	PlanID   string `json:"plan_event_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
}
type commandKey struct{}
type commandIdentity struct{ ID, Hash string }

// Start admits synchronously, then executes independently of an HTTP connection.
// The command key is persisted with acceptance; retrying an uncertain request
// returns its existing run, never repeats its effects.
func (l *Loop) Start(sid string, cmd Command, release func()) (*RunState, error) {
	l.commandMu.Lock()
	defer l.commandMu.Unlock()
	// The admitted scope must not remain aliased to the caller's slice.
	cmd.Steps = append([]int(nil), cmd.Steps...)
	if cmd.ID == "" || len(cmd.ID) > 128 {
		return nil, fmt.Errorf("command_id required (maximum 128 characters)")
	}
	switch cmd.Kind {
	case "chat":
		if strings.TrimSpace(cmd.Text) == "" {
			return nil, fmt.Errorf("text required")
		}
	case "step", "continue", "selected":
	default:
		return nil, fmt.Errorf("unknown command kind")
	}
	if cmd.Kind != "selected" && len(cmd.Steps) != 0 {
		return nil, fmt.Errorf("steps is only valid for a selected command")
	}
	if cmd.Kind == "selected" && cmd.Revision {
		return nil, fmt.Errorf("selected execution cannot request a revision; revise the target step explicitly")
	}
	if cmd.Mode != "" && cmd.Mode != "autopilot" && cmd.Mode != "discussion" && cmd.Mode != "brainstorming" {
		return nil, fmt.Errorf("invalid mode")
	}
	if cmd.Sampling != "" && cmd.Sampling != "default" && cmd.Sampling != "strict" && cmd.Sampling != "neutral" && cmd.Sampling != "creative" {
		return nil, fmt.Errorf("invalid sampling preset")
	}
	raw, _ := json.Marshal(cmd)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	events, err := episodic.Replay(l.path(sid))
	if err != nil {
		return nil, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.RunState {
			var st RunState
			if json.Unmarshal(events[i].Payload, &st) == nil && st.CommandID == cmd.ID {
				if st.CommandHash != hash {
					return nil, fmt.Errorf("command_id already used for a different request")
				}
				if release != nil {
					release()
				}
				if latest := l.RunStateOf(sid); latest != nil && latest.ID == st.ID {
					st = *latest
				}
				return &st, nil
			}
		}
	}
	if cmd.Kind != "chat" {
		p, id, e := LatestPlan(l.path(sid))
		if e != nil {
			return nil, e
		}
		if cmd.PlanID != "" && cmd.PlanID != id {
			return nil, fmt.Errorf("plan changed; refresh before running")
		}
		if cmd.Kind == "step" && (cmd.Step < -1 || cmd.Step >= len(p.Steps)) {
			return nil, fmt.Errorf("invalid step index")
		}
		if cmd.Kind == "selected" {
			if cmd.PlanID == "" {
				return nil, fmt.Errorf("plan_event_id required for selected execution")
			}
			sup, _, err := ReducePlan(events)
			if err != nil {
				return nil, err
			}
			if err := validateSelection(sup, cmd.Steps); err != nil {
				return nil, err
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	ctx = context.WithValue(WithSampling(WithLongTurn(ctx), cmd.Sampling), commandKey{}, commandIdentity{cmd.ID, hash})
	mode := ModeByName(cmd.Mode).Name
	if cmd.Kind != "chat" {
		mode = "autopilot"
	}
	ctx, h, finish, err := l.beginRun(ctx, sid, mode, cmd.Text)
	if err != nil {
		cancel()
		return nil, err
	}
	h.mu.Lock()
	accepted := h.state
	h.mu.Unlock()
	go func() {
		defer cancel()
		if release != nil {
			defer release()
		}
		var result *Result
		var runErr error
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("worker panic: %v", recovered)
			}
			finish(result, runErr)
		}()
		switch cmd.Kind {
		case "chat":
			result, runErr = l.Run(ctx, sid, cmd.Text, mode)
		case "step":
			result, runErr = l.RunStep(ctx, sid, StepRunRequest{Step: cmd.Step, Revision: cmd.Revision, PlanID: cmd.PlanID, Reason: cmd.Reason})
		case "continue":
			result, runErr = l.RunAutopilot(ctx, sid)
		case "selected":
			result, runErr = l.RunSelected(ctx, sid, cmd.PlanID, cmd.Steps)
		}
	}()
	return &accepted, nil
}
