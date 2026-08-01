package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"cerveau/internal/rfx"
)

// FuzzRun is one fuzz case's outcome.
type FuzzRun struct {
	Args   map[string]any
	Output string
	Ms     int64
	Err    error
}

// FuzzReport is the full result of fuzzing one reflex.
type FuzzReport struct {
	Reflex string
	Runs   []FuzzRun
}

// Failures returns the human-readable contract violations (empty = green).
func (r *FuzzReport) Failures() []string {
	var out []string
	for _, run := range r.Runs {
		if run.Err != nil {
			out = append(out, fmt.Sprintf("args %v: %v", run.Args, run.Err))
		}
	}
	return out
}

// dryStub records the call and returns deterministic text: the fuzz harness
// executes pipelines WITHOUT side effects (no real bash, no real writes).
type dryStub struct{ name string }

func (d *dryStub) Name() string        { return d.name }
func (d *dryStub) Description() string { return "fuzz dry-run stub" }
func (d *dryStub) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (d *dryStub) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return fmt.Sprintf("DRY-RUN tool=%s args=%s", d.name, string(args)), nil
}

// FuzzReflex executes a reflex against every generated arg set and checks
// the contract per run (spec §6). Pipelines run in a DRY-RUN registry
// (every step tool echo-stubbed — side-effect-free by construction).
// Exec reflexes run their subprocess FOR REAL in a scratch dir, bounded by
// their timeout: a stubbed exec proves nothing — the binary existing,
// accepting JSON on stdin, and producing parseable output IS the contract.
func FuzzReflex(ctx context.Context, def rfx.Reflex, argSets []map[string]any) *FuzzReport {
	rep := &FuzzReport{Reflex: def.Name}

	toolNames := map[string]bool{}
	for _, s := range def.Steps {
		toolNames[s.Tool] = true
	}
	var entries []Entry
	for name := range toolNames {
		entries = append(entries, Entry{Tool: &dryStub{name: name}, RiskTier: RiskSafe})
	}
	reg := NewRegistry(entries...)
	reg.SetGuard(func(tool string, args json.RawMessage) error { return nil })
	scratch, err := os.MkdirTemp("", "rfx-fuzz-")
	if err != nil {
		rep.Runs = append(rep.Runs, FuzzRun{Err: err})
		return rep
	}
	defer os.RemoveAll(scratch)
	reg.SetWorkspace(scratch)
	if errs := reg.AddReflexes([]rfx.Reflex{def}); len(errs) != 0 {
		for _, e := range errs {
			rep.Runs = append(rep.Runs, FuzzRun{Err: e})
		}
		return rep
	}

	for _, args := range argSets {
		raw, _ := json.Marshal(args)
		start := time.Now()
		out, runErr := reg.ExecuteMode(ctx, def.Name, raw, "")
		elapsed := time.Since(start)
		err := rfx.CheckContract(def.Contract, out, elapsed, runErr)
		rep.Runs = append(rep.Runs, FuzzRun{Args: args, Output: out, Ms: elapsed.Milliseconds(), Err: err})
	}
	return rep
}
