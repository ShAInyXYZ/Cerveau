package skills

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type SkillTool struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Command     string         `yaml:"command"`
	Schema      map[string]any `yaml:"schema"`
}

type Skill struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Triggers    []string    `yaml:"triggers"`
	Tools       []SkillTool `yaml:"tools"`
	Body        string      `yaml:"-"`
	Path        string      `yaml:"-"`
}

func Parse(data []byte, path string) (*Skill, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---") {
		return nil, nil
	}
	rest := text[3:]
	idx := strings.Index(rest, "---")
	if idx < 0 {
		return nil, nil
	}
	var s Skill
	if err := yaml.Unmarshal([]byte(rest[:idx]), &s); err != nil {
		return nil, err
	}
	if s.Name == "" {
		return nil, nil
	}
	s.Body = strings.TrimSpace(rest[idx+3:])
	s.Path = path
	return &s, nil
}

type Loader struct {
	dir string

	mu      sync.RWMutex
	skills  []Skill
	scanned time.Time
}

func NewLoader(dir string) *Loader {
	return &Loader{dir: dir}
}

func (l *Loader) List() []Skill {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Skill{}, l.skills...)
}

func (l *Loader) refresh(force bool) {
	l.mu.RLock()
	stale := time.Since(l.scanned) > 30*time.Second
	l.mu.RUnlock()
	if !stale && !force {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !force && time.Since(l.scanned) <= 30*time.Second {
		return
	}
	l.scanned = time.Now()
	l.skills = nil
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(l.dir, e.Name()))
		if err != nil {
			continue
		}
		if s, err := Parse(data, filepath.Join(l.dir, e.Name())); err == nil && s != nil {
			l.skills = append(l.skills, *s)
		}
	}
}

func (l *Loader) Match(query string) []Skill {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	lower := strings.ToLower(query)
	type scored struct {
		s     Skill
		score int
	}
	var matches []scored
	for _, s := range l.skills {
		score := 0
		for _, t := range s.Triggers {
			if t != "" && strings.Contains(lower, strings.ToLower(t)) {
				score += 3
			}
		}
		for _, w := range strings.Fields(strings.ToLower(s.Name + " " + s.Description)) {
			if len(w) > 3 && strings.Contains(lower, w) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, scored{s, score})
		}
	}
	for i := 0; i < len(matches)-1; i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].score > matches[i].score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}
	out := []Skill{}
	for i, m := range matches {
		if i >= 2 {
			break
		}
		out = append(out, m.s)
	}
	return out
}

const BodyCapChars = 1500

func (s *Skill) CappedBody() string {
	if len(s.Body) > BodyCapChars {
		return s.Body[:BodyCapChars] + "\n...[skill truncated]"
	}
	return s.Body
}
