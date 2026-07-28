package skills

import (
	"os"
	"path/filepath"
	"testing"
)

const goTestingSkill = `---
name: go-testing
description: How to run and write tests in Go projects
triggers: [test, go test, testing]
tools:
  - name: run_tests
    description: Run the project test suite
    command: "go test ./..."
    schema:
      type: object
      properties:
        pattern: {type: string}
---

# Go Testing Skill

Run tests with the run_tests tool. Prefer table-driven tests.
`

func TestParse(t *testing.T) {
	s, err := Parse([]byte(goTestingSkill), "go-testing.md")
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		t.Fatal("expected a skill, got nil")
	}
	if s.Name != "go-testing" {
		t.Fatalf("name = %q", s.Name)
	}
	if len(s.Triggers) != 3 || s.Triggers[0] != "test" {
		t.Fatalf("triggers = %v", s.Triggers)
	}
	if len(s.Tools) != 1 || s.Tools[0].Name != "run_tests" || s.Tools[0].Command != "go test ./..." {
		t.Fatalf("tools = %+v", s.Tools)
	}
	if s.Tools[0].Schema == nil {
		t.Fatal("expected tool schema")
	}
	if !contains(s.Body, "Go Testing Skill") {
		t.Fatalf("body missing markdown: %q", s.Body)
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	s, err := Parse([]byte("# just markdown, no frontmatter"), "x.md")
	if err != nil {
		t.Fatal(err)
	}
	if s != nil {
		t.Fatalf("expected nil for frontmatter-less file, got %+v", s)
	}
}

func TestMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go-testing.md"), []byte(goTestingSkill), 0o644)
	os.WriteFile(filepath.Join(dir, "docker.md"), []byte(`---
name: docker
description: Container operations
triggers: [docker, container, compose]
---
# Docker
`), 0o644)

	l := NewLoader(dir)

	hits := l.Match("please run the go test suite")
	if len(hits) == 0 || hits[0].Name != "go-testing" {
		t.Fatalf("expected go-testing top hit, got %+v", names(hits))
	}

	if hits := l.Match("what is the weather today"); len(hits) != 0 {
		t.Fatalf("expected no matches, got %v", names(hits))
	}

	if hits := l.Match("start the docker container"); len(hits) == 0 || hits[0].Name != "docker" {
		t.Fatalf("expected docker hit, got %v", names(hits))
	}
}

func TestMatchCapsAtTwo(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		os.WriteFile(filepath.Join(dir, n+".md"), []byte("---\nname: "+n+"\ndescription: test skill\ntriggers: [test]\n---\nbody"), 0o644)
	}
	l := NewLoader(dir)
	if hits := l.Match("test test test"); len(hits) > 2 {
		t.Fatalf("expected max 2 skills, got %d", len(hits))
	}
}

func TestCappedBody(t *testing.T) {
	s := &Skill{Body: string(make([]byte, BodyCapChars+500))}
	if len(s.CappedBody()) > BodyCapChars+40 {
		t.Fatalf("body not capped: %d", len(s.CappedBody()))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func names(ss []Skill) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return out
}
