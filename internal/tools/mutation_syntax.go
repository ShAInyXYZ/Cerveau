package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const mutationSyntaxTimeout = 10 * time.Second

// Only ApplyPatch can supply this capability. It authorizes one exact
// intermediate image whose final batch image has already been checked.
// Remediation, a changed path, or changed source cannot reuse the capability.
type mutationSyntaxProof struct {
	Target, BeforeSHA, AfterSHA string
}

type mutationSyntaxProofKey struct{}

func mutationSyntaxModes(path, target string) []string {
	ext := strings.ToLower(filepath.Ext(target))
	if ext != ".js" && ext != ".mjs" && ext != ".cjs" {
		ext = strings.ToLower(filepath.Ext(path))
	}
	switch ext {
	case ".mjs":
		return []string{"module"}
	case ".cjs":
		return []string{"commonjs"}
	case ".js":
		// Do not guess package.json settings or execute project configuration.
		// Either JavaScript grammar is accepted; this is not a module-mode check.
		return []string{"module", "commonjs"}
	default:
		return nil
	}
}

func validateMutationSyntax(ctx context.Context, j jail, path, before, after string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target, err := j.resolve(path)
	if err != nil {
		return "", err
	}
	target, err = evalExistingPrefix(target)
	if err != nil {
		return "", err
	}
	if proof, ok := ctx.Value(mutationSyntaxProofKey{}).(mutationSyntaxProof); ok {
		if proof.Target != target || proof.BeforeSHA != sourceSHA256(before) || proof.AfterSHA != sourceSHA256(after) {
			return "", fmt.Errorf("projected patch image changed after syntax validation; no edit applied; re-read and resubmit the complete patch")
		}
		return "syntax: exact intermediate image authorized by final batch validation; behavior checks not run\n", nil
	}
	return newMutationSyntaxValidator().validate(ctx, j.root, path, target, before, after)
}

type mutationSyntaxValidator struct {
	lookup func(string) (string, error)
	run    func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult
}

func newMutationSyntaxValidator() mutationSyntaxValidator {
	return mutationSyntaxValidator{lookup: exec.LookPath, run: runNativeProcess}
}

type mutationSyntaxReceipt struct {
	Path         string `json:"path"`
	BeforeSHA    string `json:"before_sha256"`
	ProjectedSHA string `json:"projected_sha256"`
	Status       string `json:"status"`
	Scope        string `json:"scope"`
	Diagnostic   string `json:"diagnostic,omitempty"`
}

func (v mutationSyntaxValidator) validate(ctx context.Context, workspace, path, target, before, after string) (string, error) {
	modes := mutationSyntaxModes(path, target)
	if len(modes) == 0 {
		return "syntax unverified: only .js/.mjs/.cjs are supported; behavior checks not run\n", nil
	}
	r := mutationSyntaxReceipt{Path: path, BeforeSHA: sourceSHA256(before), ProjectedSHA: sourceSHA256(after), Status: "unverified", Scope: "Node parse only; .js accepts module or CommonJS; no project code, imports or behavior checks executed"}
	finish := func(err error) (string, error) {
		raw, _ := json.Marshal(r)
		return "[syntax " + string(raw) + "]\n", err
	}
	if len(after) > maxFileSize || len(before) > maxFileSize {
		r.Diagnostic = "source exceeds the 1 MiB syntax validation bound"
		return finish(fmt.Errorf("syntax validation unavailable for %s: %s; no edits applied", path, r.Diagnostic))
	}
	program, err := v.lookup("node")
	if err != nil {
		r.Diagnostic = "installed Node runtime unavailable; no installation attempted"
		return finish(fmt.Errorf("syntax validation unavailable for %s: %s; no edits applied", path, r.Diagnostic))
	}
	program, err = filepath.EvalSymlinks(program)
	root, rootErr := filepath.EvalSymlinks(workspace)
	if err != nil || rootErr != nil || root == string(filepath.Separator) || newJail(root).contains(program) {
		r.Diagnostic = "syntax parser must be an installed runtime outside the workspace"
		return finish(fmt.Errorf("syntax validation unavailable for %s: %s; no edits applied", path, r.Diagnostic))
	}
	ctx, cancel := context.WithTimeout(ctx, mutationSyntaxTimeout)
	defer cancel()
	valid, diagnostic, err := v.parse(ctx, workspace, program, path, modes, after)
	if err != nil {
		r.Diagnostic = diagnostic
		return finish(fmt.Errorf("syntax validation unavailable for %s: %w; no edits applied", path, err))
	}
	if valid {
		r.Status = "pass"
		return finish(nil)
	}
	r.Diagnostic = diagnostic
	baselineValid, _, err := v.parse(ctx, workspace, program, path, modes, before)
	if err != nil {
		return finish(fmt.Errorf("baseline syntax validation unavailable for %s: %w; no edits applied", path, err))
	}
	if baselineValid {
		r.Status = "rejected"
		return finish(fmt.Errorf("syntax regression: %s; no edits applied. Submit complete syntax in one edit or one apply_patch batch, including matching function/block boundaries; then rerun the unchanged checks", diagnostic))
	}
	r.Status = "baseline_invalid"
	r.Diagnostic = "baseline and projected source both have syntax errors; repair allowed without syntax-pass credit: " + diagnostic
	return finish(nil)
}

var mutationNodeSyntaxError = regexp.MustCompile(`(?m)^SyntaxError: ([^\r\n]+)$`)
var mutationNodeSyntaxLine = regexp.MustCompile(`^\[stdin\]:([0-9]+)\r?\n`)

func (v mutationSyntaxValidator) parse(ctx context.Context, workspace, program, path string, modes []string, source string) (bool, string, error) {
	var diagnostic string
	for _, mode := range modes {
		if err := ctx.Err(); err != nil {
			return false, "syntax parser timed out or was canceled", err
		}
		p := v.run(ctx, workspace, program, []string{"--check", "--input-type=" + mode}, []byte(source), mutationSyntaxTimeout, true)
		if err := ctx.Err(); err != nil {
			return false, "syntax parser timed out or was canceled", err
		}
		if p.TimedOut || p.ExitCode < 0 || p.StdoutTruncated || p.StderrTruncated || (p.Err != nil && p.ExitCode == 0) {
			return false, "syntax parser/sandbox did not complete with bounded output", fmt.Errorf("syntax parser/sandbox did not complete with bounded output")
		}
		if p.ExitCode == 0 && strings.TrimSpace(p.Stdout) == "" && strings.TrimSpace(p.Stderr) == "" {
			return true, "", nil
		}
		location := mutationNodeSyntaxLine.FindStringSubmatch(p.Stderr)
		errors := mutationNodeSyntaxError.FindAllStringSubmatch(p.Stderr, -1)
		if p.ExitCode != 1 || strings.TrimSpace(p.Stdout) != "" || len(location) == 0 || len(errors) == 0 {
			return false, "Node returned unsupported syntax-check output", fmt.Errorf("Node returned unsupported syntax-check output")
		}
		if diagnostic == "" {
			diagnostic = path + ":" + location[1] + ": " + diagnosticShorten(errors[len(errors)-1][1], 500)
		}
	}
	return false, diagnostic, nil
}
