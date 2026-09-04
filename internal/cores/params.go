package cores

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Overrides are the runtime parameters a user changed for one Core from the
// panel. They live in ~/.config/cerveau/cores.d/<id>.env, an EnvironmentFile
// the Core's systemd unit reads when it starts. systemd gives those values
// precedence over the unit's own Environment= lines, so:
//
//   - a change applies on the NEXT start — a park/wake cycle or a manual
//     restart — and never reaches an engine that is already running;
//   - the file holds only what differs from the profile, so deleting it (or
//     saving an empty set) returns the Core to exactly what install.sh wrote;
//   - the harness still never runs systemctl. It writes a file; systemd reads it.

func OverridesDir() string {
	if p := os.Getenv("CRV_CORES_D"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cerveau", "cores.d")
}

func OverridesPath(id string) string {
	return filepath.Join(OverridesDir(), id+".env")
}

// EmbedOverridesPath holds the user's changes to a Core's embedder settings.
func EmbedOverridesPath(id string) string {
	return filepath.Join(OverridesDir(), id+".embed.env")
}

// EmbedEnvPath is the file cerveau-embed.service reads (EnvironmentFile=).
// Restart writes the ACTIVE Core's effective embed settings here, so the
// embedder follows the profile: CPU for a one-GPU box, the 3060 on the rig.
func EmbedEnvPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cerveau", "embed.env")
}

// WriteEmbedEnv writes the effective embed settings for the embedder unit.
// An empty set removes the file, which returns the unit to its own defaults.
func WriteEmbedEnv(eff map[string]string) error {
	return SaveOverrides(EmbedEnvPath(), eff)
}

var keyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// ValidateOverride refuses what would break the unit or the proxy behind it.
// PORT is fixed: the socket proxy forwards to it, and a Core that moved port
// would wake into a void.
func ValidateOverride(k, v string) error {
	if !keyRe.MatchString(k) {
		return fmt.Errorf("parameter %q: not an environment name", k)
	}
	if k == "PORT" {
		return fmt.Errorf("PORT is fixed by the profile's socket proxy")
	}
	if strings.ContainsAny(v, "\n\r\"\\") || len(v) > 200 {
		return fmt.Errorf("parameter %s: value must be one line without quotes, at most 200 chars", k)
	}
	return nil
}

// LoadOverrides reads KEY=VALUE lines. A missing file is an empty set.
func LoadOverrides(path string) (map[string]string, error) {
	ov := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ov, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		ov[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return ov, sc.Err()
}

// SaveOverrides writes the set, validating every entry first so a bad key
// never lands on disk. An empty set removes the file: the profile is the
// truth again, and there is nothing stale for the unit to read.
func SaveOverrides(path string, ov map[string]string) error {
	for k, v := range ov {
		if err := ValidateOverride(k, v); err != nil {
			return err
		}
	}
	if len(ov) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	keys := make([]string, 0, len(ov))
	for k := range ov {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Written by Cerveau (Settings → Engine → Profile). Read by the Core's\n")
	b.WriteString("# systemd unit at start; these override the unit's Environment= lines.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, ov[k])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// Effective is what the Core will run with on its next start: the profile's
// defaults with the overrides applied. Override keys the profile does not
// declare are kept — a launcher can read more than its unit spells out.
func Effective(defaults, ov map[string]string) map[string]string {
	eff := make(map[string]string, len(defaults)+len(ov))
	for k, v := range defaults {
		eff[k] = v
	}
	for k, v := range ov {
		eff[k] = v
	}
	return eff
}
