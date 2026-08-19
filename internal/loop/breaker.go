package loop

import (
	"fmt"
	"regexp"
	"strings"
)

// A model that hits a missing dependency does not experience it as a wall. Each
// retry is a fresh idea to it — a different flag, a different package name, a
// different path — so it can spend an entire run circling one absent thing.
//
// The v10 fan run: 43 bash calls, 1 write, no fan. 26 of those calls hunted for
// playwright/puppeteer, which Cerveau has never had. Nothing in the harness
// ever said "the thing you are reaching for is not here."
//
// The breaker counts failures by SHAPE rather than by exact command, because
// the model varies the command every time while the underlying failure stays
// identical. Three of a shape and the tool stops returning an error and starts
// returning a question.

type bashBreaker struct {
	fails map[string]int
}

func newBashBreaker() *bashBreaker { return &bashBreaker{fails: map[string]int{}} }

var (
	// the missing thing is what matters, not the command that revealed it
	missingModule = regexp.MustCompile(`(?i)cannot find module ['"]?([\w@/.-]+)`)
	notFoundCmd   = regexp.MustCompile(`(?i)([\w.-]+): (?:command not found|not found)`)
	noSuchFile    = regexp.MustCompile(`(?i)no such file or directory`)
)

// shape reduces a (command, error) pair to what is actually stuck. Two calls
// share a shape when they fail for the same underlying reason, even if the
// commands differ entirely.
func (b *bashBreaker) shape(cmd, out string) string {
	if m := missingModule.FindStringSubmatch(out); m != nil {
		// playwright-core and playwright are the same wall
		return "missing-module:" + strings.SplitN(strings.TrimPrefix(m[1], "@"), "-", 2)[0]
	}
	if m := notFoundCmd.FindStringSubmatch(out); m != nil {
		return "missing-command:" + m[1]
	}
	if noSuchFile.MatchString(out) {
		return "missing-path"
	}
	// fall back to the first word of the command: repeated `npm ...` failures
	// with varied errors still count as circling npm
	fields := strings.Fields(strings.TrimPrefix(cmd, "cd "))
	if len(fields) == 0 {
		return "empty"
	}
	for _, f := range fields {
		if !strings.Contains(f, "/") && !strings.HasPrefix(f, "-") && f != "&&" {
			return "cmd:" + f
		}
	}
	return "cmd:" + fields[0]
}

// record notes a failure. Returns a hint and true once this shape has failed
// enough times that retrying is no longer a plan.
func (b *bashBreaker) record(cmd, out string) (string, bool) {
	s := b.shape(cmd, out)
	b.fails[s]++
	if b.fails[s] < 3 {
		return "", false
	}
	b.fails[s] = 0 // one prompt per streak, not one per call from here on
	return breakerHint(s), true
}

// ok clears a shape: the model got past it, so the streak is over.
func (b *bashBreaker) ok(cmd string) {
	delete(b.fails, b.shape(cmd, ""))
}

func breakerHint(shape string) string {
	what := "that command"
	switch {
	case strings.HasPrefix(shape, "missing-module:"):
		what = "the module `" + strings.TrimPrefix(shape, "missing-module:") + "`"
	case strings.HasPrefix(shape, "missing-command:"):
		what = "the command `" + strings.TrimPrefix(shape, "missing-command:") + "`"
	case shape == "missing-path":
		what = "that path"
	}
	return fmt.Sprintf("STOP — this has now failed three times for the same reason: %s is not "+
		"available here. Do NOT try another variation of it. Answer these before your next "+
		"call: (1) Is %s actually installed on this machine, or are you assuming it is? "+
		"(2) What tool that you ALREADY have does the same job — check_page can load a page, "+
		"run JS in it with `eval`, and report console errors, with no browser driver needed. "+
		"(3) Is this verification step even required to finish the task? Pick a different way "+
		"or move on.", what, what)
}
