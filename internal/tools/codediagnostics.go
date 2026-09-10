package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	diagnosticMaxFiles       = 16
	diagnosticMaxSourceBytes = 1 << 20
	diagnosticMaxTotalBytes  = 8 << 20
	diagnosticMaxRawBytes    = 128 << 10
	diagnosticMaxFindings    = 128
)

// CodeDiagnostics exposes fixed, installed checker adapters. Its receipt only
// verifies the named files under the stated adapter semantics, never an entire
// project, dependency graph, test suite, or running application.
type CodeDiagnostics struct {
	j        jail
	lookPath func(string) (string, error)
	run      func(context.Context, string, string, []string, []byte, time.Duration, bool) nativeProcessResult
}

func NewCodeDiagnostics(workspace string) *CodeDiagnostics {
	if real, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = real
	}
	return &CodeDiagnostics{j: newJail(workspace), lookPath: exec.LookPath, run: runNativeProcess}
}

func (t *CodeDiagnostics) Name() string { return "code_diagnostics" }

func (t *CodeDiagnostics) Description() string {
	return "Run bounded, read-only native diagnostics on explicitly named workspace files using installed tools. " +
		"checker=node syntax-checks JS/MJS/CJS without running source (.js defaults to ES modules); " +
		"typescript uses workspace node_modules/typescript/bin/tsc --noEmit --pretty false with explicit files and compiler defaults, ignoring tsconfig; " +
		"go_vet checks explicit .go files in one directory with go vet -json. No installs, arbitrary commands, flags, environment, or network. " +
		"Returns located diagnostics, check identity, source hashes before/after, exit/timeout evidence and bounded raw output. " +
		"Full retained evidence is saved in an exclusive .devcheck/code-diagnostics receipt; the bounded response includes its path, SHA-256 and whole diagnostic previews with explicit omission counts. " +
		"Only a completed clean invocation can pass; missing dependencies, incomplete parsing, timeouts and changed source are unverified. " +
		"A pass covers only this adapter and the named files, not project configuration, dependency freshness, tests, or runtime behavior."
}

func (t *CodeDiagnostics) Schema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"checker": map[string]any{"type": "string", "enum": []string{"node", "typescript", "go_vet"}},
			"paths": map[string]any{"type": "array", "minItems": 1, "maxItems": diagnosticMaxFiles, "uniqueItems": true,
				"items": map[string]any{"type": "string", "maxLength": 1024}, "description": "Explicit workspace-relative files, no directories, globs, flags, parent traversal, or response files. Go files must share a directory."},
			"timeout_ms": map[string]any{"type": "integer", "minimum": 100, "maximum": 60000, "default": 20000, "description": "Total subprocess time budget for this call."},
			"js_mode":    map[string]any{"type": "string", "enum": []string{"module", "commonjs"}, "default": "module", "description": "Node only: syntax mode for .js; .mjs always module, .cjs always commonjs."},
		},
		"required": []string{"checker", "paths"},
	}
}

type diagnosticArgs struct {
	Checker   string   `json:"checker"`
	Paths     []string `json:"paths"`
	TimeoutMS *int     `json:"timeout_ms"`
	JSMode    string   `json:"js_mode"`
}

type diagnosticSource struct {
	Path         string `json:"path"`
	SHA256Before string `json:"sha256_before"`
	SHA256After  string `json:"sha256_after"`
	Unchanged    bool   `json:"unchanged"`
	Error        string `json:"error,omitempty"`
}

type diagnosticFinding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
	Check    string `json:"check"`
}

type diagnosticInvocation struct {
	Check              string              `json:"check"`
	Path               string              `json:"path,omitempty"`
	Executable         string              `json:"executable"`
	Args               []string            `json:"args"`
	Status             string              `json:"status"`
	Reason             string              `json:"reason"`
	ParseComplete      bool                `json:"parse_complete"`
	ExitCode           int                 `json:"exit_code"`
	TimedOut           bool                `json:"timed_out"`
	ElapsedMS          int64               `json:"elapsed_ms"`
	Stdout             string              `json:"stdout"`
	Stderr             string              `json:"stderr"`
	StdoutTruncated    bool                `json:"stdout_truncated"`
	StderrTruncated    bool                `json:"stderr_truncated"`
	RawOutputShortened bool                `json:"raw_output_shortened"`
	ProcessError       string              `json:"process_error,omitempty"`
	Diagnostics        []diagnosticFinding `json:"diagnostics"`
}

type diagnosticReport struct {
	SchemaVersion int                    `json:"schema_version"`
	Tool          string                 `json:"tool"`
	Checker       string                 `json:"checker"`
	CheckID       string                 `json:"check_id"`
	Status        string                 `json:"status"`
	Complete      bool                   `json:"complete"`
	Reason        string                 `json:"reason"`
	Scope         string                 `json:"scope"`
	Sources       []diagnosticSource     `json:"sources"`
	Diagnostics   []diagnosticFinding    `json:"diagnostics"`
	Checks        []diagnosticInvocation `json:"checks"`
	Limitations   []string               `json:"limitations"`
	ReceiptPath   string                 `json:"receipt_path"`
}

func (t *CodeDiagnostics) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	a, err := diagnosticDecodeArgs(raw)
	if err != nil {
		return "", err
	}
	paths, contents, sources, err := t.diagnosticSources(a)
	if err != nil {
		return "", err
	}
	report := diagnosticReport{
		SchemaVersion: 1, Tool: t.Name(), Checker: a.Checker,
		Status: "unverified", Reason: "checker did not complete", Scope: "explicit_files",
		Sources: sources, Diagnostics: []diagnosticFinding{}, Checks: []diagnosticInvocation{},
		Limitations: []string{"Only the explicitly named source files are fingerprinted; imported dependencies and the installed toolchain are not freshness-verified.", "A pass is not evidence of project-wide correctness, test success, or runtime behavior."},
	}
	identity, _ := json.Marshal(struct {
		Checker, JSMode string
		Sources         []diagnosticSource
	}{a.Checker, a.JSMode, sources})
	report.CheckID = "code_diagnostics:" + diagnosticSHA(identity)
	switch a.Checker {
	case "node":
		report.Limitations = append(report.Limitations, "Syntax only: source is read through stdin, imports are not resolved and code is not executed. .js uses the explicit js_mode (module by default), independently of package.json.")
	case "typescript":
		report.Limitations = append(report.Limitations, "Explicit file mode uses the installed compiler's default options and ignores tsconfig.json. Project references, project settings and framework-specific checks are not covered; unsupported compiler versions/configuration return unverified.")
	case "go_vet":
		report.Limitations = append(report.Limitations, "Only the specified Go files form the checked package; omitted sibling files/packages and tests are not checked. Installed Go tooling and cached dependencies are required; downloads and workspace configuration are disabled.")
	}
	finish := func() (string, error) {
		for i := range report.Sources {
			full, resolveErr := t.j.resolve(report.Sources[i].Path)
			var data []byte
			if resolveErr == nil {
				data, resolveErr = diagnosticReadSource(full)
			}
			if resolveErr != nil {
				report.Sources[i].Error = diagnosticShorten(resolveErr.Error(), 1024)
			} else {
				report.Sources[i].SHA256After = diagnosticSHA(data)
				report.Sources[i].Unchanged = report.Sources[i].SHA256Before == report.Sources[i].SHA256After
			}
			if !report.Sources[i].Unchanged {
				report.Status, report.Complete, report.Reason = "unverified", false, "source changed or became unreadable during diagnostics"
			}
		}
		return t.diagnosticFinish(&report)
	}
	executableName := "node"
	if a.Checker == "go_vet" {
		executableName = "go"
	}
	executable, err := t.lookPath(executableName)
	if err != nil {
		report.Reason = "installed " + executableName + " runtime unavailable"
		return finish()
	}
	var commandArgs []string
	switch a.Checker {
	case "typescript":
		compiler, resolveErr := t.j.resolve("node_modules/typescript/bin/tsc")
		if resolveErr != nil {
			report.Reason = "local TypeScript compiler resolves outside the workspace or is unreadable"
			return finish()
		}
		info, statErr := os.Stat(compiler)
		if statErr != nil || !info.Mode().IsRegular() {
			report.Reason = "local TypeScript compiler missing: node_modules/typescript/bin/tsc; no installation attempted"
			return finish()
		}
		commandArgs = append([]string{compiler, "--noEmit", "--pretty", "false", "--incremental", "false"}, paths...)
	case "go_vet":
		commandArgs = append([]string{"vet", "-json", "-mod=readonly", "-p=1"}, paths...)
	}
	timeout := 20 * time.Second
	if a.TimeoutMS != nil {
		timeout = time.Duration(*a.TimeoutMS) * time.Millisecond
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := checkCtx.Deadline()
	count := 1
	if a.Checker == "node" {
		count = len(paths)
	}
	for i := 0; i < count; i++ {
		remaining := time.Until(deadline)
		if remaining <= 0 || checkCtx.Err() != nil {
			report.Reason = "diagnostics canceled or total timeout reached before all checks completed"
			return finish()
		}
		var stdin []byte
		path := ""
		if a.Checker == "node" {
			mode := a.JSMode
			switch filepath.Ext(paths[i]) {
			case ".mjs":
				mode = "module"
			case ".cjs":
				mode = "commonjs"
			}
			commandArgs = []string{"--check", "--input-type=" + mode}
			stdin, path = contents[i], sources[i].Path
		}
		process := t.run(checkCtx, t.j.root, executable, commandArgs, stdin, remaining, true)
		rawShortened := len(process.Stdout) > diagnosticMaxRawBytes || len(process.Stderr) > diagnosticMaxRawBytes
		if len(process.Stdout) > diagnosticMaxRawBytes {
			process.Stdout = diagnosticShorten(process.Stdout, diagnosticMaxRawBytes)
			process.StdoutTruncated = true
		}
		if len(process.Stderr) > diagnosticMaxRawBytes {
			process.Stderr = diagnosticShorten(process.Stderr, diagnosticMaxRawBytes)
			process.StderrTruncated = true
		}
		findings, parsed := diagnosticParse(t.j, a.Checker, path, process.Stdout, process.Stderr)
		invocation := diagnosticInvocation{
			Check: a.Checker, Path: path, Executable: executable, Args: append([]string(nil), commandArgs...),
			ExitCode: process.ExitCode, TimedOut: process.TimedOut, ElapsedMS: process.Elapsed.Milliseconds(),
			Stdout: process.Stdout, Stderr: process.Stderr,
			StdoutTruncated: process.StdoutTruncated, StderrTruncated: process.StderrTruncated,
			RawOutputShortened: rawShortened,
			ParseComplete:      parsed, Diagnostics: findings,
		}
		if process.Err != nil {
			invocation.ProcessError = diagnosticShorten(process.Err.Error(), 1024)
		}
		switch {
		case process.TimedOut || checkCtx.Err() != nil || errors.Is(process.Err, context.Canceled) || errors.Is(process.Err, context.DeadlineExceeded):
			invocation.Status, invocation.Reason = "unverified", "checker timed out or was canceled"
		case process.StdoutTruncated || process.StderrTruncated || !parsed:
			invocation.Status, invocation.Reason = "unverified", "checker output is truncated, unsupported, or reports unavailable dependencies/configuration"
		case process.ExitCode < 0 || (process.Err != nil && process.ExitCode == 0):
			invocation.Status, invocation.Reason = "unverified", "checker process did not complete successfully"
		case len(findings) > 0:
			invocation.Status, invocation.Reason = "fail", "checker reported diagnostics"
		case process.ExitCode != 0 || process.Err != nil:
			invocation.Status, invocation.Reason = "unverified", "nonzero checker exit without fully parsed diagnostics"
		default:
			invocation.Status, invocation.Reason = "pass", "checker exited zero with complete clean output"
		}
		report.Checks = append(report.Checks, invocation)
		if len(report.Diagnostics)+len(findings) > diagnosticMaxFindings {
			report.Diagnostics = append(report.Diagnostics, findings[:diagnosticMaxFindings-len(report.Diagnostics)]...)
			report.Reason = "diagnostic count exceeded the retained evidence limit"
			return finish()
		}
		report.Diagnostics = append(report.Diagnostics, findings...)
	}
	report.Status, report.Complete, report.Reason = "pass", true, "all requested checks completed with zero exit and complete clean output"
	for _, check := range report.Checks {
		if check.Status == "unverified" {
			report.Status, report.Complete, report.Reason = "unverified", false, check.Reason
			break
		}
		if check.Status == "fail" {
			report.Status, report.Reason = "fail", "one or more checks reported diagnostics"
		}
	}
	return finish()
}

func diagnosticDecodeArgs(raw json.RawMessage) (diagnosticArgs, error) {
	var a diagnosticArgs
	if len(raw) > 32<<10 {
		return a, errors.New("code_diagnostics arguments exceed 32 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return a, fmt.Errorf("invalid code_diagnostics arguments: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return a, errors.New("code_diagnostics requires one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return a, errors.New("code_diagnostics requires a JSON object")
	}
	if field, present := fields["timeout_ms"]; present && bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
		return a, errors.New("timeout_ms must be an integer when supplied")
	}
	if _, present := fields["js_mode"]; present && a.JSMode == "" {
		return a, errors.New("js_mode must be module or commonjs when supplied")
	}
	if a.Checker != "node" && a.Checker != "typescript" && a.Checker != "go_vet" {
		return a, errors.New("checker must be node, typescript, or go_vet")
	}
	if len(a.Paths) < 1 || len(a.Paths) > diagnosticMaxFiles {
		return a, fmt.Errorf("paths requires 1 to %d explicit files", diagnosticMaxFiles)
	}
	if a.TimeoutMS != nil && (*a.TimeoutMS < 100 || *a.TimeoutMS > 60000) {
		return a, errors.New("timeout_ms must be between 100 and 60000")
	}
	if a.JSMode != "" && a.Checker != "node" {
		return a, errors.New("js_mode is only valid for the node checker")
	}
	if a.JSMode == "" {
		a.JSMode = "module"
	}
	if a.JSMode != "module" && a.JSMode != "commonjs" {
		return a, errors.New("js_mode must be module or commonjs")
	}
	return a, nil
}

func (t *CodeDiagnostics) diagnosticSources(a diagnosticArgs) ([]string, [][]byte, []diagnosticSource, error) {
	var paths []string
	var contents [][]byte
	var sources []diagnosticSource
	seen := make(map[string]bool)
	total := 0
	goDir := ""
	for _, path := range a.Paths {
		if err := diagnosticValidatePath(path); err != nil {
			return nil, nil, nil, err
		}
		full, err := t.j.resolve(path)
		if err != nil {
			return nil, nil, nil, err
		}
		real, err := filepath.EvalSymlinks(full)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("diagnostic source unavailable: %w", err)
		}
		if seen[real] {
			return nil, nil, nil, errors.New("duplicate diagnostic source paths are not allowed")
		}
		seen[real] = true
		ext := filepath.Ext(full)
		valid := a.Checker == "node" && (ext == ".js" || ext == ".mjs" || ext == ".cjs") ||
			a.Checker == "typescript" && (ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".cts") ||
			a.Checker == "go_vet" && ext == ".go"
		if !valid {
			return nil, nil, nil, fmt.Errorf("unsupported file extension %q for %s", ext, a.Checker)
		}
		if a.Checker == "go_vet" {
			if goDir != "" && goDir != filepath.Dir(full) {
				return nil, nil, nil, errors.New("go_vet explicit files must share one directory")
			}
			goDir = filepath.Dir(full)
		}
		data, err := diagnosticReadSource(full)
		if err != nil {
			return nil, nil, nil, err
		}
		total += len(data)
		if total > diagnosticMaxTotalBytes {
			return nil, nil, nil, errors.New("diagnostic sources exceed 8 MiB total")
		}
		relative, _ := filepath.Rel(t.j.root, full)
		paths, contents = append(paths, full), append(contents, data)
		sources = append(sources, diagnosticSource{Path: filepath.ToSlash(relative), SHA256Before: diagnosticSHA(data)})
	}
	return paths, contents, sources, nil
}

func diagnosticValidatePath(path string) error {
	if path == "" || len(path) > 1024 || filepath.IsAbs(path) || strings.ContainsAny(path, "\\;|&$`<>*?[]{}!:\"'") || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return errors.New("diagnostic paths must be plain workspace-relative file paths")
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." || strings.HasPrefix(part, "-") || strings.HasPrefix(part, "@") {
			return errors.New("diagnostic paths cannot contain parent traversal, flags, or response files")
		}
	}
	return nil
}

func diagnosticReadSource(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > diagnosticMaxSourceBytes {
		return nil, errors.New("diagnostic source must be a regular file no larger than 1 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("diagnostic source changed or is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, diagnosticMaxSourceBytes+1))
	if err == nil && len(data) > diagnosticMaxSourceBytes {
		err = errors.New("diagnostic source grew beyond 1 MiB")
	}
	return data, err
}

func diagnosticSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func diagnosticShorten(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "")
}
