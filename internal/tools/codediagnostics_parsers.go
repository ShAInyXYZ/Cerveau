package tools

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	diagnosticNodeLocation = regexp.MustCompile(`^\[stdin\]:(\d+)$`)
	diagnosticNodeCaret    = regexp.MustCompile(`^[ \t]*\^+[ \t]*$`)
	diagnosticNodeVersion  = regexp.MustCompile(`^Node\.js v\d+\.\d+\.\d+(?:[-+].*)?$`)
	diagnosticTSLocated    = regexp.MustCompile(`^(.+)\((\d+),(\d+)\): (error|warning) (TS\d+): (.+)$`)
	diagnosticTSGlobal     = regexp.MustCompile(`^(error|warning) (TS\d+): (.+)$`)
	diagnosticGoLocated    = regexp.MustCompile(`^(?:vet: )?(.+\.go):(\d+)(?::(\d+))?: (.+)$`)
	diagnosticGoPosition   = regexp.MustCompile(`^(.+):(\d+):(\d+)$`)
)

// Parsing is deliberately closed: unknown output is retained in the receipt,
// but it cannot establish a successful check. In particular go vet -json may
// exit zero when it has findings, so process status alone is insufficient.
func diagnosticParse(j jail, checker, path, stdout, stderr string) ([]diagnosticFinding, bool) {
	var findings []diagnosticFinding
	var complete bool
	switch checker {
	case "node":
		findings, complete = diagnosticParseNode(path, stdout, stderr)
	case "typescript":
		findings, complete = diagnosticParseTS(j, stdout, stderr)
	case "go_vet":
		findings, complete = diagnosticParseGo(j, stdout, stderr)
	default:
		return []diagnosticFinding{}, false
	}
	if findings == nil {
		findings = []diagnosticFinding{}
	}
	if len(findings) > diagnosticMaxFindings {
		findings, complete = findings[:diagnosticMaxFindings], false
	}
	for i := range findings {
		if len(findings[i].Message) > 4096 {
			findings[i].Message = diagnosticShorten(findings[i].Message, 4096)
			complete = false
		}
	}
	return findings, complete
}

func diagnosticParseNode(path, stdout, stderr string) ([]diagnosticFinding, bool) {
	complete := strings.TrimSpace(stdout) == ""
	text := strings.Trim(strings.ReplaceAll(stderr, "\r\n", "\n"), "\n")
	if text == "" {
		return nil, complete
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 4 {
		return nil, false
	}
	location := diagnosticNodeLocation.FindStringSubmatch(lines[0])
	if location == nil || !diagnosticNodeCaret.MatchString(lines[2]) {
		return nil, false
	}
	line, err := strconv.Atoi(location[1])
	if err != nil || line <= 0 {
		return nil, false
	}
	i := 3
	for i < len(lines) && lines[i] == "" {
		i++
	}
	if i == len(lines) || !strings.HasPrefix(lines[i], "SyntaxError: ") {
		return nil, false
	}
	message := strings.TrimPrefix(lines[i], "SyntaxError: ")
	if message == "" {
		return nil, false
	}
	finding := diagnosticFinding{
		File: path, Line: line, Column: utf8.RuneCountInString(lines[2][:strings.IndexByte(lines[2], '^')]) + 1,
		Severity: "error", Message: message, Code: "SyntaxError", Check: "node/syntax",
	}
	versionSeen := false
	for _, tail := range lines[i+1:] {
		switch {
		case tail == "":
		case !versionSeen && strings.HasPrefix(tail, "    at "):
		case !versionSeen && diagnosticNodeVersion.MatchString(tail):
			versionSeen = true
		default:
			complete = false
		}
	}
	return []diagnosticFinding{finding}, complete && versionSeen
}

func diagnosticParseTS(j jail, stdout, stderr string) ([]diagnosticFinding, bool) {
	var findings []diagnosticFinding
	complete := true
	for _, stream := range []string{stdout, stderr} {
		continuation := -1
		for _, line := range strings.Split(strings.ReplaceAll(stream, "\r\n", "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if match := diagnosticTSLocated.FindStringSubmatch(line); match != nil {
				file, ok := diagnosticLocationFile(j, match[1])
				row, rowErr := strconv.Atoi(match[2])
				col, colErr := strconv.Atoi(match[3])
				if !ok || rowErr != nil || colErr != nil || row <= 0 || col <= 0 {
					complete, continuation = false, -1
					continue
				}
				findings = append(findings, diagnosticFinding{File: file, Line: row, Column: col, Severity: match[4], Code: match[5], Message: match[6], Check: "typescript/tsc"})
				continuation = len(findings) - 1
				if diagnosticMissingDependency(match[5], match[6]) {
					complete = false
				}
				continue
			}
			if match := diagnosticTSGlobal.FindStringSubmatch(line); match != nil {
				findings = append(findings, diagnosticFinding{Severity: match[1], Code: match[2], Message: match[3], Check: "typescript/tsc"})
				// These normally indicate compiler options, environment or missing
				// libraries, not a located finding in checked source.
				complete, continuation = false, len(findings)-1
				continue
			}
			if continuation >= 0 && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
				findings[continuation].Message += "\n" + line
				continue
			}
			complete, continuation = false, -1
		}
	}
	return findings, complete
}

func diagnosticMissingDependency(code, message string) bool {
	switch code {
	case "TS2307", "TS2318", "TS2580", "TS2591", "TS2593", "TS2688", "TS2792", "TS5012", "TS5083", "TS6053", "TS6231", "TS7016":
		return true
	}
	lower := strings.ToLower(message)
	for _, marker := range []string{"cannot find module", "cannot find package", "could not import", "no required module provides package", "module lookup disabled", "cannot find type definition", "could not read", "no such file or directory"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func diagnosticParseGo(j jail, stdout, stderr string) ([]diagnosticFinding, bool) {
	var findings []diagnosticFinding
	complete := true
	for _, stream := range []string{stdout, stderr} {
		text := strings.TrimSpace(stream)
		for text != "" {
			if strings.HasPrefix(text, "# ") {
				_, rest, found := strings.Cut(text, "\n")
				if !found {
					complete = false
					break
				}
				text = strings.TrimSpace(rest)
				if text == "" {
					complete = false
				}
				continue
			}
			if strings.HasPrefix(text, "{") {
				decoder := json.NewDecoder(strings.NewReader(text))
				var packages map[string]map[string]json.RawMessage
				if err := decoder.Decode(&packages); err != nil {
					complete = false
					break
				}
				text = strings.TrimSpace(text[decoder.InputOffset():])
				packageNames := make([]string, 0, len(packages))
				for name := range packages {
					packageNames = append(packageNames, name)
				}
				sort.Strings(packageNames)
				for _, name := range packageNames {
					analyzers := packages[name]
					if analyzers == nil {
						complete = false
						continue
					}
					analyzerNames := make([]string, 0, len(analyzers))
					for analyzer := range analyzers {
						analyzerNames = append(analyzerNames, analyzer)
					}
					sort.Strings(analyzerNames)
					for _, analyzer := range analyzerNames {
						var entries []struct {
							Position string `json:"posn"`
							Message  string `json:"message"`
						}
						if err := json.Unmarshal(analyzers[analyzer], &entries); err != nil || entries == nil {
							complete = false
							continue
						}
						for _, entry := range entries {
							match := diagnosticGoPosition.FindStringSubmatch(entry.Position)
							if match == nil || entry.Message == "" {
								complete = false
								continue
							}
							file, ok := diagnosticLocationFile(j, match[1])
							row, rowErr := strconv.Atoi(match[2])
							col, colErr := strconv.Atoi(match[3])
							if !ok || rowErr != nil || colErr != nil || row <= 0 || col <= 0 {
								complete = false
								continue
							}
							findings = append(findings, diagnosticFinding{File: file, Line: row, Column: col, Severity: "error", Message: entry.Message, Check: "go_vet/" + analyzer})
							if diagnosticMissingDependency("", entry.Message) {
								complete = false
							}
						}
					}
				}
				continue
			}
			line, rest, _ := strings.Cut(text, "\n")
			text = strings.TrimSpace(rest)
			if match := diagnosticGoLocated.FindStringSubmatch(line); match != nil {
				file, ok := diagnosticLocationFile(j, match[1])
				row, rowErr := strconv.Atoi(match[2])
				col := 0
				var colErr error
				if match[3] != "" {
					col, colErr = strconv.Atoi(match[3])
				}
				if !ok || rowErr != nil || colErr != nil || row <= 0 || col < 0 {
					complete = false
					continue
				}
				findings = append(findings, diagnosticFinding{File: file, Line: row, Column: col, Severity: "error", Message: match[4], Check: "go_vet/compile"})
				if diagnosticMissingDependency("", match[4]) {
					complete = false
				}
			} else {
				complete = false
			}
		}
	}
	return findings, complete
}

func diagnosticLocationFile(j jail, path string) (string, bool) {
	if filepath.IsAbs(path) {
		if !j.contains(filepath.Clean(path)) {
			return "", false
		}
		var err error
		path, err = filepath.Rel(j.root, path)
		if err != nil {
			return "", false
		}
	}
	if err := diagnosticValidatePath(path); err != nil {
		return "", false
	}
	full, err := j.resolve(path)
	if err != nil {
		return "", false
	}
	relative, err := filepath.Rel(j.root, full)
	return filepath.ToSlash(relative), err == nil
}
