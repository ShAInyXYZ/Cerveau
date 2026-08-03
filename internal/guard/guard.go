package guard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	TierCatastrophic = "catastrophic"
	TierSensitive    = "sensitive"
)

type rule struct {
	tier   string
	re     *regexp.Regexp
	reason string
	hint   string
}

type Guard struct {
	mu        sync.RWMutex
	workspace string
	cmdRules  []rule
	pathRules []rule
}

// SetWorkspace follows a runtime workspace switch. The guard used to be
// frozen at its startup root, so after a switch it judged rm paths against a
// STALE workspace and blocked legitimate in-project deletes as "outside the
// workspace".
func (g *Guard) SetWorkspace(ws string) {
	g.mu.Lock()
	g.workspace = ws
	g.mu.Unlock()
}

func (g *Guard) ws() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.workspace
}

func New(workspace string) *Guard {
	return &Guard{
		workspace: workspace,
		cmdRules: []rule{
			// rm -r/-f is judged by rmViolation (workspace-aware, in Check):
			// the old regex here branded ANY absolute path catastrophic, which
			// blocked legitimate in-project deletes like rm -rf <ws>/dist.
			{TierCatastrophic, regexp.MustCompile(`:\(\)\s*\{`), "fork bomb", "never allowed"},
			{TierCatastrophic, regexp.MustCompile(`\bdd\b[^|]*\bof=/dev/`), "dd writing to a device", "never allowed"},
			{TierCatastrophic, regexp.MustCompile(`\bmkfs[.\s]`), "filesystem format", "never allowed"},
			{TierCatastrophic, regexp.MustCompile(`\b(shutdown|reboot|poweroff|halt)\b`), "system power operation", "never allowed"},
			{TierCatastrophic, regexp.MustCompile(`\bgit\s+push\b[^|]*(--force\b|-f\b)`), "force push rewrites remote history", "never allowed — use a normal push"},
			{TierCatastrophic, regexp.MustCompile(`(?i)\bdrop\s+(database|table)\b`), "DROP on a database object", "never allowed"},
			{TierCatastrophic, regexp.MustCompile(`>\s*/dev/(sd|nvme|hd)`), "raw write to a disk device", "never allowed"},
			{TierCatastrophic, regexp.MustCompile(`\bchmod\s+(-R\s+)?777\s+/(\s|$)`), "chmod 777 on root", "never allowed"},
			{TierSensitive, regexp.MustCompile(`\bgit\s+push\b`), "push publishes to a remote", "external side effect — needs user confirmation"},
			{TierSensitive, regexp.MustCompile(`\b(npm|pip|cargo|gem)\s+publish\b`), "package publish", "external side effect — needs user confirmation"},
			{TierSensitive, regexp.MustCompile(`(curl|wget)\b[^|]*\|\s*(sudo\s+)?(ba|z|fi)?sh\b`), "piping remote script into a shell", "download first, review, then run"},
			{TierSensitive, regexp.MustCompile(`\b(ssh|scp|rsync)\b[^|]*@`), "remote connection", "remote operations need user confirmation"},
		},
		pathRules: []rule{
			{TierSensitive, regexp.MustCompile(`(^|[\s/"'=])\.env($|[.\s:"'/])`), "environment file may contain secrets", "use config example files instead"},
			{TierSensitive, regexp.MustCompile(`(^|/)\.ssh(/|$)|id_rsa|id_ed25519`), "SSH material", "never touch SSH keys"},
			{TierSensitive, regexp.MustCompile(`(?i)\.(pem|key|p12|pfx)$`), "key/certificate file", "never touch key material"},
			{TierSensitive, regexp.MustCompile(`(?i)(credential|secret|token)`), "possible secrets file", "verify the path is not a secrets store"},
		},
	}
}

func (g *Guard) Check(tool string, args json.RawMessage) error {
	switch tool {
	case "bash":
		var a struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return fmt.Errorf("bad args: %w", err)
		}
		if why := rmViolation(g.ws(), a.Command); why != "" {
			return deny(&rule{TierCatastrophic, nil, "recursive force-delete outside the workspace", why})
		}
		if r := match(g.cmdRules, a.Command); r != nil {
			return deny(r)
		}
		if r := match(g.pathRules, a.Command); r != nil {
			return deny(r)
		}
	case "read", "edit", "write", "grep":
		var a struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &a); err == nil && a.Path != "" {
			if r := match(g.pathRules, a.Path); r != nil {
				return deny(r)
			}
		}
	}
	return nil
}

func match(rules []rule, s string) *rule {
	for _, r := range rules {
		if r.re.MatchString(s) {
			return &r
		}
	}
	return nil
}

// TierError is a guard denial carrying its tier, so callers can tell
// "needs the user's confirmation" (sensitive) from "never" (catastrophic)
// without string-matching.
type TierError struct {
	Tier   string
	Reason string
	Hint   string
}

func (e *TierError) Error() string {
	return fmt.Sprintf("[%s] blocked: %s — %s", e.Tier, e.Reason, e.Hint)
}

func deny(r *rule) error {
	return &TierError{Tier: r.tier, Reason: r.reason, Hint: r.hint}
}

// rmInvocation finds rm calls that carry -r or -f flags and captures their
// target list (up to a shell separator).
var rmInvocation = regexp.MustCompile(`(?:^|[;&|]\s*|\bsudo\s+)rm\s+((?:-\S+\s+)*)([^|;&]*)`)

// rmViolation judges an rm against the REAL workspace boundary. An absolute
// path inside the workspace is an ordinary delete; root, home, bare globs,
// traversal, unresolvable variables, and anything outside the workspace are
// catastrophic. Empty string = no violation.
func rmViolation(workspace, cmd string) string {
	if workspace == "" {
		return ""
	}
	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	for _, m := range rmInvocation.FindAllStringSubmatch(cmd, -1) {
		flags, rest := m[1], m[2]
		if !strings.ContainsAny(flags, "rf") && !strings.Contains(flags, "-R") {
			continue // plain rm without -r/-f keeps its old (unguarded) behavior
		}
		for _, tok := range strings.Fields(rest) {
			tok = strings.Trim(tok, `"'`)
			if tok == "" || strings.HasPrefix(tok, "-") {
				continue
			}
			// expand the only variables we can verify
			switch {
			case tok == "~" || tok == "$HOME" || tok == "${HOME}":
				tok = home
			case strings.HasPrefix(tok, "~/"):
				tok = filepath.Join(home, tok[2:])
			case strings.HasPrefix(tok, "$HOME/"):
				tok = filepath.Join(home, tok[6:])
			case strings.Contains(tok, "$"):
				return fmt.Sprintf("cannot verify %q (shell variable) — use an explicit path inside the workspace", tok)
			}
			// a bare glob deletes the whole cwd — treat like deleting the workspace root
			if tok == "*" || tok == "/*" {
				return "bare glob would delete everything at the root — name the paths explicitly"
			}
			glob := strings.Split(tok, "*")[0] // judge the fixed prefix of a glob
			full := glob
			if !filepath.IsAbs(full) {
				full = filepath.Join(wsAbs, full)
			}
			full = filepath.Clean(full)
			if full != wsAbs && !strings.HasPrefix(full, wsAbs+string(filepath.Separator)) {
				return fmt.Sprintf("%q resolves outside the workspace (%s) — delete inside the workspace only", tok, wsAbs)
			}
			if full == wsAbs && strings.Contains(tok, "*") {
				return "glob at the workspace root would delete everything — name the paths explicitly"
			}
		}
	}
	return ""
}
