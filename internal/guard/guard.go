package guard

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	workspace string
	cmdRules  []rule
	pathRules []rule
}

func New(workspace string) *Guard {
	return &Guard{
		workspace: workspace,
		cmdRules: []rule{
			{TierCatastrophic, regexp.MustCompile(`\brm\s+(-\w*[rf]\w*\s+)+\s*(/|~|\*)[/*]*\s*$`), "recursive force-delete of a root path", "never allowed — delete inside the workspace only"},
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

func deny(r *rule) error {
	return fmt.Errorf("[%s] blocked: %s — %s", r.tier, r.reason, r.hint)
}
