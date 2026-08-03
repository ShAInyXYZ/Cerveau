package tools

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ParsedStep mirrors commit_plan's step shape, produced from plain markdown.
type ParsedStep struct {
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	Files  []string `json:"files"`
	Risk   string   `json:"risk"`
}

var (
	rePlanH1       = regexp.MustCompile(`(?m)^#\s+(.+)$`)
	rePlanH2       = regexp.MustCompile(`(?m)^##+\s+(.+)$`)
	rePlanNumbered = regexp.MustCompile(`(?m)^\s*\d+[.)]\s+(.+)$`)
	rePlanCheckbox = regexp.MustCompile(`(?m)^\s*[-*]\s*\[[ xX]?\]\s+(.+)$`)
	reBacktickPath = regexp.MustCompile("`([\\w./-]+\\.[\\w]+)`")
	// plain (unbackticked) path-shaped tokens: either dir/file.ext, or a bare
	// filename with a code-ish extension. Bare words like "cannon-es" or
	// version numbers never match.
	rePlainPath = regexp.MustCompile(`(?m)(?:^|[\s(])([\w.-]+(?:/[\w.-]+)+\.(?:js|ts|html|css|json|svelte|go|py|md)|(?:index|main|app|style|styles)\.(?:js|ts|html|css))(?:$|[\s),])`)
)

// ParsePlanMarkdown turns a markdown plan — the model's NATURAL output — into
// a title and structured steps. It accepts the three shapes small models
// actually write: ## section headings, numbered lists, and checkboxes.
// Backticked file paths inside a step become its files list.
func ParsePlanMarkdown(md string) (string, []ParsedStep) {
	title := ""
	if m := rePlanH1.FindStringSubmatch(md); m != nil {
		title = strings.TrimSpace(m[1])
	}

	// Preferred shape: ## headings with body text as detail.
	if locs := rePlanH2.FindAllStringSubmatchIndex(md, -1); len(locs) >= 2 {
		var steps []ParsedStep
		for i, loc := range locs {
			end := len(md)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			body := strings.TrimSpace(md[loc[1]:end])
			steps = append(steps, ParsedStep{
				Title:  strings.TrimSpace(md[loc[2]:loc[3]]),
				Detail: firstLines(body, 3),
				Files:  backtickedFiles(body),
			})
		}
		return title, steps
	}

	// Checkboxes, then numbered lists.
	for _, re := range []*regexp.Regexp{rePlanCheckbox, rePlanNumbered} {
		if ms := re.FindAllStringSubmatch(md, -1); len(ms) >= 2 {
			var steps []ParsedStep
			for _, m := range ms {
				line := strings.TrimSpace(m[1])
				steps = append(steps, ParsedStep{Title: line, Files: backtickedFiles(line)})
			}
			return title, steps
		}
	}
	return title, nil
}

// PlanLike reports whether a written file is a plan document: a markdown file
// whose NAME suggests a plan, containing at least two step-shaped entries.
// The name gate is deliberate — READMEs and docs have headings too; only a
// file the model itself called a plan is treated as one.
func PlanLike(path, content string) bool {
	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		return false
	}
	base := strings.ToLower(filepath.Base(path))
	if !strings.Contains(base, "plan") && !strings.Contains(base, "roadmap") && !strings.Contains(base, "sprint") {
		return false
	}
	_, steps := ParsePlanMarkdown(content)
	return len(steps) >= 2
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func backtickedFiles(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reBacktickPath.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	for _, m := range rePlainPath.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}
