package cores

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Switching Cores keeps the same split as parking: Cerveau decides, the park
// watchdog acts. The panel's Restart button makes the API write a request
// file; the watchdog stops every other Core unit, starts the chosen one, and
// restarts Cerveau so the LLM client and the window pick up the endpoint.
// The harness itself never runs systemctl — if it dies mid-switch, systemd
// still owns both ends, and the socket still wakes whichever Core cores.json
// names.
//
// Both files live where the park request lives: XDG_RUNTIME_DIR (tmpfs), so a
// stale request cannot survive a reboot and switch a Core nobody asked for.

type SwitchRequest struct {
	Core           string   `json:"core"`
	Unit           string   `json:"unit"`
	Stop           []string `json:"stop"`            // every other Core's unit
	RestartCerveau bool     `json:"restart_cerveau"` // the endpoint changed
	RestartEmbed   bool     `json:"restart_embed"`   // embed.env was rewritten
	RequestedAt    string   `json:"requested_at"`
}

// SwitchStatus is what the watchdog writes as it works, so the panel can show
// "stopping vLLM · Dense → starting BF16 → restarting Cerveau" instead of a
// spinner over a socket that is silently loading 55 GB.
type SwitchStatus struct {
	Core      string `json:"core"`
	Phase     string `json:"phase"` // queued | stopping | starting | restarting | done | failed
	Detail    string `json:"detail,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

func runDir() string {
	if run := os.Getenv("XDG_RUNTIME_DIR"); run != "" {
		return filepath.Join(run, "cerveau")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".crv", "run")
}

func SwitchRequestPath() string { return filepath.Join(runDir(), "switch-request.json") }
func SwitchStatusPath() string  { return filepath.Join(runDir(), "switch-status.json") }

// WriteSwitchRequest queues the switch and marks it queued, so the panel sees
// a state before the watchdog's next poll picks the request up.
func WriteSwitchRequest(req SwitchRequest) error {
	if err := os.MkdirAll(runDir(), 0o755); err != nil {
		return err
	}
	req.RequestedAt = time.Now().Format(time.RFC3339)
	data, _ := json.MarshalIndent(req, "", "  ")
	if err := os.WriteFile(SwitchRequestPath(), data, 0o644); err != nil {
		return err
	}
	st, _ := json.Marshal(SwitchStatus{Core: req.Core, Phase: "queued", UpdatedAt: req.RequestedAt})
	return os.WriteFile(SwitchStatusPath(), st, 0o644)
}

// ReadSwitchStatus returns the last switch's status, or nil when there is none
// or it is older than an hour — a finished switch from yesterday is not news.
func ReadSwitchStatus() *SwitchStatus {
	data, err := os.ReadFile(SwitchStatusPath())
	if err != nil {
		return nil
	}
	var st SwitchStatus
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, st.UpdatedAt); err == nil && time.Since(t) > time.Hour {
		return nil
	}
	return &st
}
