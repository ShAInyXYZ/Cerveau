// rfx.go — crvcli rfx: local reflex management. NO server required (T0):
// everything operates on ~/.crv/rfx/ through internal/rfx directly.
//
//	crvcli rfx list                # valid reflexes + rejected files with reasons
//	crvcli rfx show <name>         # one reflex, expanded
//	crvcli rfx install <file>      # validate, then copy into ~/.crv/rfx/
//	crvcli rfx remove <name>       # delete a reflex file
//	crvcli rfx test <name|file>    # parse + validate (fuzz execution: m9-fuzz)
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cerveau/internal/rfx"
	"cerveau/internal/tools"
)

// rfxDir is overridable for tests.
func rfxDir() string {
	if v := os.Getenv("CRV_RFX_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".crv/rfx"
	}
	return filepath.Join(home, ".crv", "rfx")
}

// knownCoreTool mirrors the core registry set for offline validation. The
// live server may have more (code tools, remember); unknown-to-us names are
// still accepted via the loader's reflex-name pass, and the server re-checks
// at load anyway — a wrong accept here is caught loudly on the next turn.
func knownCoreTool(name string) bool {
	switch name {
	case "bash", "read", "write", "edit", "grep", "glob", "web_fetch", "ask_user", "commit_plan", "remember",
		"file_map", "find_symbol", "find_references", "outline_file":
		return true
	}
	return false
}

func (c *client) cmdRfx(args []string) error {
	if len(args) == 0 {
		rfxUsage()
		return fmt.Errorf("rfx: subcommand required")
	}
	switch args[0] {
	case "list":
		return rfxList()
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("rfx show: name required")
		}
		return rfxShow(args[1])
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("rfx install: file required")
		}
		return rfxInstall(args[1])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("rfx remove: name required")
		}
		return rfxRemove(args[1])
	case "test":
		if len(args) < 2 {
			return fmt.Errorf("rfx test: name or file required")
		}
		return rfxTest(args[1])
	case "distill":
		if len(args) < 2 {
			return fmt.Errorf("rfx distill: skill name or .md path required")
		}
		return c.rfxDistill(args[1])
	default:
		rfxUsage()
		return fmt.Errorf("rfx: unknown subcommand %q", args[0])
	}
}

func rfxList() error {
	l := rfx.NewLoader(rfxDir(), knownCoreTool)
	defs := l.List()
	errs := l.Errors()
	fmt.Printf("%-20s %-9s %-10s %-24s %s\n", "NAME", "KIND", "RISK", "MODES", "DESCRIPTION")
	for _, d := range defs {
		modes := strings.Join(d.Modes, ",")
		if modes == "" {
			modes = "all"
		}
		fmt.Printf("%-20s %-9s %-10s %-24s %s\n", d.Name, d.Kind, d.Risk, modes, d.Description)
	}
	for _, e := range errs {
		fmt.Printf("REJECTED %-14s %s: %v\n", filepath.Base(e.Path), "", e.Err)
	}
	if len(defs) == 0 && len(errs) == 0 {
		fmt.Println("(no reflexes in " + rfxDir() + ")")
	}
	return nil
}

func rfxShow(name string) error {
	l := rfx.NewLoader(rfxDir(), knownCoreTool)
	d, ok := l.Get(name)
	if !ok {
		return fmt.Errorf("no valid reflex named %q (check 'crvcli rfx list' for rejections)", name)
	}
	fmt.Printf("name:        %s\nkind:        %s\nrisk:        %s\ndescription: %s\n", d.Name, d.Kind, d.Risk, d.Description)
	fmt.Printf("modes:       %s\ningress_cap: %d\ncard:        fs=%v network=%v env=%v subprocess=%v\n",
		orAll(d.Modes), d.Cap(), d.Card.FS, d.Card.Network, d.Card.Env, d.Card.Subprocess)
	if d.Contract.MaxMs > 0 || d.Contract.OutputRegex != "" || len(d.Contract.MustNotContain) > 0 {
		fmt.Printf("contract:    max_ms=%d regex=%q must_not_contain=%v\n", d.Contract.MaxMs, d.Contract.OutputRegex, d.Contract.MustNotContain)
	}
	if d.Kind == rfx.KindExec {
		fmt.Printf("argv:        %v\ntimeout:     %s\n", d.Argv, d.Timeout)
	}
	for i, s := range d.Steps {
		fmt.Printf("step %d:      %s", i+1, s.Tool)
		if s.ID != "" {
			fmt.Printf(" (id %s)", s.ID)
		}
		if s.When != "" {
			fmt.Printf(" [when %s]", s.When)
		}
		if s.Optional {
			fmt.Print(" [optional]")
		}
		fmt.Printf("\n           %v\n", s.Args)
	}
	fmt.Println("file:        " + d.Path)
	return nil
}

func rfxInstall(src string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// Validate against the DESTINATION path so the stem rule checks what
	// the file will actually be named.
	base := filepath.Base(src)
	if !strings.HasSuffix(base, ".rfx.yaml") {
		return fmt.Errorf("%s: reflex files must end in .rfx.yaml", base)
	}
	r, err := rfx.Parse(data, filepath.Join(rfxDir(), base))
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if err := rfx.Validate(r, knownCoreTool); err != nil {
		return fmt.Errorf("invalid reflex, NOT installed:\n  %w", err)
	}
	dst := filepath.Join(rfxDir(), base)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists — 'crvcli rfx remove %s' first (no silent overwrites)", base, r.Name)
	}
	if err := os.MkdirAll(rfxDir(), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("installed %s (%s, %s) — live on the next turn\n", r.Name, r.Kind, r.Risk)
	return nil
}

func rfxRemove(name string) error {
	path := filepath.Join(rfxDir(), name+".rfx.yaml")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no reflex file %s", path)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	fmt.Printf("removed %s — gone from the grammar on the next turn\n", name)
	return nil
}

func rfxTest(target string) error {
	// Accept a name from the dir or a direct file path.
	path := target
	if !strings.HasSuffix(target, ".rfx.yaml") {
		path = filepath.Join(rfxDir(), target+".rfx.yaml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	r, err := rfx.Parse(data, path)
	if err != nil {
		return fmt.Errorf("FAIL parse: %v", err)
	}
	if err := rfx.Validate(r, knownCoreTool); err != nil {
		return fmt.Errorf("FAIL validate: %v", err)
	}
	fmt.Printf("PASS validate: %s (%s, risk %s, %d steps, cap %d)\n", r.Name, r.Kind, r.Risk, len(r.Steps), r.Cap())

	// Fuzz (spec §6): generated arg sets, dry-run registry, contract checks.
	argSets := rfx.GenerateArgs(r.Params, 100)
	rep := tools.FuzzReflex(context.Background(), *r, argSets)
	failures := rep.Failures()
	if len(failures) > 0 {
		fmt.Printf("FAIL fuzz: %d/%d runs violated the contract:\n", len(failures), len(rep.Runs))
		for i, f := range failures {
			if i >= 5 {
				fmt.Printf("  … and %d more\n", len(failures)-5)
				break
			}
			fmt.Printf("  - %s\n", f)
		}
		return fmt.Errorf("fuzz failed for %s", r.Name)
	}
	fmt.Printf("PASS fuzz: %d/%d runs green", len(rep.Runs), len(rep.Runs))
	if r.Contract.MaxMs > 0 {
		fmt.Printf(" (max_ms=%d)", r.Contract.MaxMs)
	}
	fmt.Println()
	return nil
}

func orAll(modes []string) string {
	if len(modes) == 0 {
		return "all"
	}
	return strings.Join(modes, ",")
}

func rfxUsage() {
	fmt.Fprint(os.Stderr, `crvcli rfx — local reflex management (no server needed)

  crvcli rfx list                valid reflexes + rejected files
  crvcli rfx show <name>         one reflex, expanded
  crvcli rfx install <file>      validate + copy into ~/.crv/rfx/
  crvcli rfx remove <name>       delete a reflex
  crvcli rfx test <name|file>    validate + fuzz against spec v1
  crvcli rfx distill <skill>      convert a prose skill to a draft reflex (needs server)
`)
}
