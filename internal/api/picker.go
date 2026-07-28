package api

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// pickDirectory opens a native folder-selection dialog and returns the chosen
// absolute path. Works because crv runs on the user's own machine. Tries the
// common Linux dialog tools; returns an error if none is available or the user
// cancels (exit code 1).
func pickDirectory(start string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	type dialog struct {
		bin  string
		args []string
	}
	candidates := []dialog{
		{"zenity", []string{"--file-selection", "--directory", "--title=Select workspace folder", "--filename=" + ensureTrailingSlash(start)}},
		{"qarma", []string{"--file-selection", "--directory", "--title=Select workspace folder"}},
		{"kdialog", []string{"--getexistingdirectory", start}},
	}

	for _, d := range candidates {
		if _, err := exec.LookPath(d.bin); err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, d.bin, d.args...).Output()
		if err != nil {
			// exit 1 = user cancelled; treat as a clean cancel
			return "", fmt.Errorf("cancelled or failed: %w", err)
		}
		path := strings.TrimSpace(string(out))
		if path == "" {
			return "", fmt.Errorf("no directory chosen")
		}
		return path, nil
	}
	return "", fmt.Errorf("no native folder dialog available (install zenity or kdialog)")
}

func ensureTrailingSlash(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}
