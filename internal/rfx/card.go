package rfx

import (
	"fmt"
	"net/url"
	"strings"
)

// CheckStep enforces a reflex's capability card against one step's args
// BEFORE dispatch (spec §5). Metadata, in Go, never prompt text.
//
// Honest scope (consistent with SECURITY.md): what v1 enforces here is
//   - card.network for web_fetch steps (exact host match against the allowlist)
//   - card.fs "none" blocking file-tool steps outright
//
// File-tool workspace containment itself is enforced by the fs tools' own
// symlink-walking jail (stronger than anything the card could add). Hard
// network/fs enforcement for bash and exec subprocesses is the Landlock
// jail's job (M10+): a process can open raw sockets, and we say so.
func CheckStep(card Card, tool string, args map[string]any) error {
	if isFileTool(tool) && cardFSNone(card) {
		return fmt.Errorf("card violation: %s step blocked — card.fs is [none], this reflex may not touch files", tool)
	}
	if tool == "web_fetch" {
		raw, _ := args["url"].(string)
		return checkNetwork(card, raw)
	}
	return nil
}

func isFileTool(tool string) bool {
	switch tool {
	case "read", "write", "edit", "grep":
		return true
	}
	return false
}

func cardFSNone(card Card) bool {
	for _, f := range card.FS {
		if f == "none" {
			return true
		}
	}
	return false
}

// NetworkAllowed reports whether the card permits contacting host (used by
// the Synapse executor for exec tools too).
func NetworkAllowed(card Card, host string) bool {
	for _, n := range card.Network {
		switch n {
		case "any":
			return true
		case "none":
			return false
		default:
			if hostMatches(n, host) {
				return true
			}
		}
	}
	return false
}

func checkNetwork(card Card, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("card check: unparseable url %q", rawURL)
	}
	host := u.Hostname()
	if !NetworkAllowed(card, host) {
		return fmt.Errorf("card violation: web_fetch to %q blocked — card.network is %v (best-effort for bash/exec; exact for web_fetch)", host, []string(card.Network))
	}
	return nil
}

func hostMatches(pattern, host string) bool {
	// Allow "example.com:8080" patterns to match host without port and
	// vice versa: compare on hostname basis.
	if i := strings.LastIndex(pattern, ":"); i > 0 {
		pattern = pattern[:i]
	}
	return strings.EqualFold(pattern, host)
}
