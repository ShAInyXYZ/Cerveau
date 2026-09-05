package plan

import (
	"strings"
	"testing"
)

func TestVerifyValidate(t *testing.T) {
	cases := []struct {
		name string
		v    *Verify
		ok   bool
	}{
		{"nil is not a check", nil, false},
		{"no kind", &Verify{}, false},
		{"unknown kind", &Verify{Kind: "vibes"}, false},

		{"eval needs expr", &Verify{Kind: "eval", Path: "index.html"}, false},
		{"eval needs a page", &Verify{Kind: "eval", Expr: "!!x"}, false},
		{"eval on a path", &Verify{Kind: "eval", Expr: "!!document.querySelector('canvas')", Path: "index.html"}, true},
		{"eval on a url", &Verify{Kind: "eval", Expr: "window.__state.speed>0", URL: "http://127.0.0.1:8000/i.html"}, true},

		{"command needs a command", &Verify{Kind: "command"}, false},
		{"real command", &Verify{Kind: "command", Command: "node --check app.js"}, true},

		// the costume: true the moment anything writes the file
		{"test -f", &Verify{Kind: "command", Command: "test -f index.html"}, false},
		{"bracket -f", &Verify{Kind: "command", Command: "[ -f index.html ]"}, false},
		{"ls", &Verify{Kind: "command", Command: "ls index.html"}, false},
		{"stat", &Verify{Kind: "command", Command: "stat index.html"}, false},
		{"bare cat", &Verify{Kind: "command", Command: "cat index.html"}, false},
		// ...but a cat that PROVES something is fine
		{"cat into grep", &Verify{Kind: "command", Command: "cat index.html | grep -q buildFan"}, true},

		{"contains needs file", &Verify{Kind: "contains", Symbol: "buildFan"}, false},
		{"contains needs symbol", &Verify{Kind: "contains", File: "fan.js"}, false},
		{"real contains", &Verify{Kind: "contains", File: "fan.js", Symbol: "buildFan"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.v.Validate()
			if c.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("want rejected, got nil")
			}
		})
	}
}

func TestVerifyDescribe(t *testing.T) {
	if d := (&Verify{Kind: "contains", File: "fan.js", Symbol: "buildFan"}).Describe(); d == "" {
		t.Fatal("contains should describe itself")
	}
	if d := (&Verify{Kind: "command", Command: "node --check a.js"}).Describe(); d == "" {
		t.Fatal("command should describe itself")
	}
}

// The first plan the model ever wrote checks for used expr "true" on every
// step, pointed at .js files. A constant cannot fail, and check_page cannot
// render a script; both are the disk guess in a costume.
func TestEvalRejectsChecksThatCannotFail(t *testing.T) {
	bad := []Verify{
		{Kind: "eval", Expr: "true", Path: "index.html"},
		{Kind: "eval", Expr: "!!true", Path: "index.html"},
		{Kind: "eval", Expr: "(true)", Path: "index.html"},
		{Kind: "eval", Expr: "1", Path: "index.html"},
		{Kind: "eval", Expr: "'ok'", Path: "index.html"},
		{Kind: "eval", Expr: "true;", Path: "index.html"},
		// a real expression on a file the browser cannot render
		{Kind: "eval", Expr: "!!document.querySelector('canvas')", Path: "src/core/constants.js"},
	}
	for _, v := range bad {
		if err := v.Validate(); err == nil {
			t.Errorf("should be rejected: %+v", v)
		}
	}
	good := []Verify{
		{Kind: "eval", Expr: "!!document.querySelector('canvas')", Path: "index.html"},
		{Kind: "eval", Expr: "window.__state.speed > 0", URL: "http://127.0.0.1:8000/index.html"},
		{Kind: "eval", Expr: "typeof buildFan === 'function'", Path: "game.htm"},
		// a literal INSIDE a real expression is fine
		{Kind: "eval", Expr: "document.title === 'Car'", Path: "index.html"},
	}
	for _, v := range good {
		if err := v.Validate(); err != nil {
			t.Errorf("should be accepted: %+v: %v", v, err)
		}
	}
}

// A command is existence-only when EVERY segment is; one content check in the
// chain makes it real. The model's second live attempt wrote
// `test -f a && grep -q X a` for four steps and only pure `test -f` for one.
func TestExistenceRuleIsSegmentWise(t *testing.T) {
	rejected := []string{
		"test -f js/config.js && test -f js/input.js && test -f js/hud.js",
		"[ -f a.js ] ; [ -f b.js ]",
		"ls js/config.js || ls js/other.js",
	}
	for _, c := range rejected {
		if err := (&Verify{Kind: "command", Command: c}).Validate(); err == nil {
			t.Errorf("pure existence must be refused: %q", c)
		}
	}
	accepted := []string{
		"test -f js/core/Renderer.js && grep -q 'export class Renderer' js/core/Renderer.js",
		"grep -q 'MAX_SPEED' js/config.js && grep -q 'export class Input' js/input.js",
		"test -f a.js && node --check a.js",
	}
	for _, c := range accepted {
		if err := (&Verify{Kind: "command", Command: c}).Validate(); err != nil {
			t.Errorf("a chain with a content check is real: %q: %v", c, err)
		}
	}
}

// kind "command" with a symbol and no command is a contains-check wearing the
// wrong label; the refusal must say so.
func TestCommandWithSymbolIsRedirectedToContains(t *testing.T) {
	err := (&Verify{Kind: "command", Symbol: "export class Input", URL: "js/input.js"}).Validate()
	if err == nil || !strings.Contains(err.Error(), `kind "contains"`) {
		t.Errorf("want a hint towards contains, got %v", err)
	}
}
