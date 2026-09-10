package idle

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// ServiceState only reads systemd metadata. An HTTP probe could activate a
// parked Core's socket, so it cannot be used to answer whether it is idle.
func ServiceState(ctx context.Context, unit string) State {
	if !strings.HasSuffix(unit, ".service") || strings.HasPrefix(unit, "-") || strings.ContainsAny(unit, "/\x00\n\r") {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "show", unit,
		"--property=LoadState,ActiveState,SubState,MainPID,Result").Output()
	if err != nil {
		return ""
	}
	return serviceState(string(out))
}

func serviceState(out string) State {
	p := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			p[k] = v
		}
	}
	if p["LoadState"] != "loaded" {
		return ""
	}
	switch p["ActiveState"] {
	case "inactive":
		if p["MainPID"] == "0" && p["SubState"] == "dead" && p["Result"] == "success" {
			return Parked
		}
		return Unavailable
	case "activating", "reloading":
		return Waking
	case "active":
		return Active
	case "failed":
		return Unavailable
	}
	return "" // stopping/unknown is not confirmation that unloading finished
}
