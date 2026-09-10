package tools

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var runChecksTAPResult = regexp.MustCompile(`^(\s*)(not ok|ok)\s+(\d+)(?:\s+-\s+|\s+)(.*)$`)
var runChecksTAPPlan = regexp.MustCompile(`^1\.\.(\d+)(?:\s+#.*)?$`)
var runChecksGoLocation = regexp.MustCompile(`^\s*([^\s:]+\.go:\d+)(?::\d+)?:`)

// Parse only the TAP result lines and their attached YAML diagnostic records.
// Console assertions or arbitrary PASS strings do not become test results.
func parseRunChecksTAP(output string) ([]runChecksTest, bool) {
	tests := []runChecksTest{}
	lines := strings.Split(output, "\n")
	version, plan, roots, observed, bailed := false, -1, 0, 0, false
	for i, line := range lines {
		if line == "TAP version 13" {
			version = true
		}
		if strings.HasPrefix(strings.TrimSpace(line), "Bail out!") {
			bailed = true
		}
		if m := runChecksTAPPlan.FindStringSubmatch(line); m != nil {
			plan, _ = strconv.Atoi(m[1])
		}
		m := runChecksTAPResult.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if len(m[1]) == 0 {
			roots++
		}
		name, status := strings.TrimSpace(m[4]), "pass"
		if m[2] == "not ok" {
			status = "fail"
		}
		if at := strings.Index(strings.ToUpper(name), " # SKIP"); at >= 0 {
			name = name[:at]
			status = "skip"
		}
		if at := strings.Index(strings.ToUpper(name), " # TODO"); at >= 0 {
			name = name[:at]
			status = "skip"
		}
		if status != "skip" {
			observed++
		}
		item := runChecksTest{Name: name, Status: status, Evidence: line}
		// Node writes YAML immediately after a failing result. Keep the original
		// record as evidence even when its values cannot be decoded safely.
		if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "---" {
			start, end := i+2, i+2
			for end < len(lines) && strings.TrimSpace(lines[end]) != "..." {
				end++
			}
			if end < len(lines) {
				block := strings.Join(lines[start:end], "\n")
				item.Evidence += "\n" + strings.Join(lines[i+1:end+1], "\n")
				var diagnostic struct {
					Location string    `yaml:"location"`
					Expected yaml.Node `yaml:"expected"`
					Actual   yaml.Node `yaml:"actual"`
				}
				if yaml.Unmarshal([]byte(block), &diagnostic) == nil {
					item.Location = diagnostic.Location
					encode := func(n yaml.Node) json.RawMessage {
						if n.Kind == 0 {
							return nil
						}
						var value any
						if n.Decode(&value) != nil {
							return nil
						}
						b, err := json.Marshal(value)
						if err != nil {
							return nil
						}
						return b
					}
					item.Expected = encode(diagnostic.Expected)
					item.Actual = encode(diagnostic.Actual)
				}
			}
		}
		tests = append(tests, item)
	}
	return tests, version && !bailed && plan > 0 && plan == roots && observed > 0
}

// Go's test2json provides names and action states, but arbitrary t.Errorf text
// has no typed expected/actual format. Preserve it instead of inventing one.
func parseRunChecksGo(output string) ([]runChecksTest, bool) {
	tests := []runChecksTest{}
	index := map[string]int{}
	packages := map[string]bool{}
	invalid, observed := false, 0
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Action  string
			Package string
			Test    string
			Output  string
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Action == "" {
			invalid = true
			continue
		}
		if event.Package != "" {
			if _, ok := packages[event.Package]; !ok {
				packages[event.Package] = false
			}
			if event.Test == "" && (event.Action == "pass" || event.Action == "fail" || event.Action == "skip") {
				packages[event.Package] = true
			}
		}
		if event.Test == "" {
			continue
		}
		key := event.Package + "\x00" + event.Test
		i, ok := index[key]
		if !ok {
			i = len(tests)
			index[key] = i
			tests = append(tests, runChecksTest{Name: event.Test, Package: event.Package, Status: "unverified"})
		}
		switch event.Action {
		case "pass", "fail", "skip":
			tests[i].Status = event.Action
		case "output":
			if len(tests[i].Evidence) < 16<<10 {
				tests[i].Evidence += event.Output
				if len(tests[i].Evidence) > 16<<10 {
					tests[i].Evidence = tests[i].Evidence[:16<<10]
				}
			}
			if tests[i].Location == "" {
				if m := runChecksGoLocation.FindStringSubmatch(event.Output); m != nil {
					tests[i].Location = m[1]
				}
			}
		}
	}
	complete := !invalid && len(tests) > 0 && len(packages) > 0
	for _, test := range tests {
		if test.Status == "unverified" {
			complete = false
		}
		if test.Status == "pass" || test.Status == "fail" {
			observed++
		}
	}
	for _, done := range packages {
		if !done {
			complete = false
		}
	}
	return tests, complete && observed > 0
}
