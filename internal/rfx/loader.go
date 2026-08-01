package rfx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LoadError is one rejected manifest. Load errors are collected and surfaced
// (panel notices, crvcli rfx list) — a bad file never crashes boot and never
// silently disappears (spec: load-time rejection, loud).
type LoadError struct {
	Path string
	Err  error
}

// stateFile records enable/disable toggles OUTSIDE the manifests (user
// content is never edited to change state).
type stateFile struct {
	Disabled []string `json:"disabled"`
}

// Loader discovers and validates ~/.crv/rfx — flat reflex files AND one
// level of pack folders (v1.1). Works at T0 — no model, no Typesense
// required. Rescans at most every 30s.
type Loader struct {
	dir   string
	known KnownTool

	mu       sync.RWMutex
	reflexes []Reflex
	packs    []Pack
	disabled map[string]bool
	notices  []string
	errors   []LoadError
	scanned  time.Time
}

func NewLoader(dir string, known KnownTool) *Loader {
	return &Loader{dir: dir, known: known}
}

// List returns the valid, ENABLED reflexes — the set that enters the grammar.
func (l *Loader) List() []Reflex {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []Reflex
	for _, r := range l.reflexes {
		if !l.disabled[r.Name] {
			out = append(out, r)
		}
	}
	return out
}

// All returns every valid reflex including disabled ones (for list/UI).
func (l *Loader) All() []Reflex {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Reflex{}, l.reflexes...)
}

// Disabled reports whether a reflex is toggled off.
func (l *Loader) Disabled(name string) bool {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.disabled[name]
}

// Packs returns the valid pack manifests.
func (l *Loader) Packs() []Pack {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Pack{}, l.packs...)
}

// Notices returns non-fatal observations (e.g. a folder without pack.yaml).
func (l *Loader) Notices() []string {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string{}, l.notices...)
}

// Errors returns the rejected manifests from the latest scan.
func (l *Loader) Errors() []LoadError {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]LoadError{}, l.errors...)
}

// Get returns one valid reflex by name (enabled or not).
func (l *Loader) Get(name string) (Reflex, bool) {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, r := range l.reflexes {
		if r.Name == name {
			return r, true
		}
	}
	return Reflex{}, false
}

// SetEnabled toggles a reflex and persists .state.json. A disabled reflex
// leaves the grammar on the next turn; its file is untouched.
func (l *Loader) SetEnabled(name string, enabled bool) error {
	l.refresh(false)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.getLocked(name); !ok {
		return fmt.Errorf("no reflex named %q", name)
	}
	l.disabled[name] = !enabled
	return l.saveStateLocked()
}

func (l *Loader) getLocked(name string) (Reflex, bool) {
	for _, r := range l.reflexes {
		if r.Name == name {
			return r, true
		}
	}
	return Reflex{}, false
}

func (l *Loader) saveStateLocked() error {
	var names []string
	for n, off := range l.disabled {
		if off {
			names = append(names, n)
		}
	}
	data, err := json.MarshalIndent(stateFile{Disabled: names}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(l.dir, ".state.json"), data, 0o644)
}

// Scan forces an immediate rescan (used by crvcli rfx test/install).
func (l *Loader) Scan() { l.refresh(true) }

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
	l.reflexes = nil
	l.packs = nil
	l.notices = nil
	l.errors = nil
	l.disabled = l.loadStateLocked()

	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return // no dir yet = no reflexes; not an error (T0-friendly)
	}

	// Gather candidate files: flat *.rfx.yaml + one level of pack folders.
	type candidate struct {
		path string
		pack string // "" = standalone
	}
	var files []candidate
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			if strings.HasSuffix(name, ".rfx.yaml") {
				files = append(files, candidate{filepath.Join(l.dir, name), ""})
			}
			continue
		}
		packDir := filepath.Join(l.dir, name)
		packYAML := filepath.Join(packDir, "pack.yaml")
		data, err := os.ReadFile(packYAML)
		if err != nil {
			l.notices = append(l.notices, fmt.Sprintf("folder %q ignored — no pack.yaml (a folder of reflexes must declare itself a pack)", name))
			continue
		}
		p, err := ParsePack(data, packYAML)
		if err != nil {
			l.errors = append(l.errors, LoadError{packYAML, err})
			continue
		}
		if err := ValidatePack(p); err != nil {
			l.errors = append(l.errors, LoadError{packYAML, err})
			continue
		}
		// Discover docs (listed, not indexed — recall-indexing lands later).
		if docs, err := os.ReadDir(filepath.Join(packDir, "docs")); err == nil {
			for _, d := range docs {
				if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
					p.Docs = append(p.Docs, d.Name())
				}
			}
		}
		l.packs = append(l.packs, *p)
		sub, _ := os.ReadDir(packDir)
		for _, se := range sub {
			if !se.IsDir() && strings.HasSuffix(se.Name(), ".rfx.yaml") {
				files = append(files, candidate{filepath.Join(packDir, se.Name()), p.Pack})
			}
		}
	}

	// Pass 1: parse everything. Pass 2: validate with a combined tool
	// predicate — core registry tools PLUS the names of all parsed reflexes,
	// so a reflex may name another reflex in its steps regardless of file
	// order (cycles are the executor's depth-cap problem, not the loader's).
	type parsedReflex struct {
		r    *Reflex
		pack string
	}
	var parsed []parsedReflex
	for _, c := range files {
		data, err := os.ReadFile(c.path)
		if err != nil {
			l.errors = append(l.errors, LoadError{c.path, err})
			continue
		}
		r, err := Parse(data, c.path)
		if err != nil {
			l.errors = append(l.errors, LoadError{c.path, err})
			continue
		}
		parsed = append(parsed, parsedReflex{r, c.pack})
	}
	reflexNames := map[string]bool{}
	for _, pr := range parsed {
		reflexNames[pr.r.Name] = true
	}
	known := func(name string) bool {
		if reflexNames[name] {
			return true
		}
		return l.known != nil && l.known(name)
	}
	seen := map[string]string{} // name -> first path, for collision reporting
	for _, pr := range parsed {
		if err := Validate(pr.r, known); err != nil {
			l.errors = append(l.errors, LoadError{pr.r.Path, err})
			continue
		}
		if first, dup := seen[pr.r.Name]; dup {
			l.errors = append(l.errors, LoadError{pr.r.Path, &DuplicateError{Name: pr.r.Name, First: first}})
			continue
		}
		seen[pr.r.Name] = pr.r.Path
		pr.r.Pack = pr.pack
		l.reflexes = append(l.reflexes, *pr.r)
	}
}

func (l *Loader) loadStateLocked() map[string]bool {
	disabled := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(l.dir, ".state.json"))
	if err != nil {
		return disabled
	}
	var sf stateFile
	if json.Unmarshal(data, &sf) == nil {
		for _, n := range sf.Disabled {
			disabled[n] = true
		}
	}
	return disabled
}

// DuplicateError reports two files claiming the same reflex name.
type DuplicateError struct{ Name, First string }

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("name %q already claimed by %s", e.Name, e.First)
}
