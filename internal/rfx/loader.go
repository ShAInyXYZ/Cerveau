package rfx

import (
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

// Loader discovers and validates ~/.crv/rfx/*.rfx.yaml. Works at T0 — no
// model, no Typesense required. Rescans at most every 30s, mirroring
// skills.Loader.
type Loader struct {
	dir   string
	known KnownTool

	mu       sync.RWMutex
	reflexes []Reflex
	errors   []LoadError
	scanned  time.Time
}

func NewLoader(dir string, known KnownTool) *Loader {
	return &Loader{dir: dir, known: known}
}

// List returns the valid reflexes from the latest scan.
func (l *Loader) List() []Reflex {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Reflex{}, l.reflexes...)
}

// Errors returns the rejected manifests from the latest scan.
func (l *Loader) Errors() []LoadError {
	l.refresh(false)
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]LoadError{}, l.errors...)
}

// Get returns one valid reflex by name.
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
	l.errors = nil

	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return // no dir yet = no reflexes; not an error (T0-friendly)
	}
	// Pass 1: parse everything. Pass 2: validate with a combined tool
	// predicate — core registry tools PLUS the names of all parsed reflexes,
	// so a reflex may name another reflex in its steps regardless of file
	// order (cycles are the executor's depth-cap problem, not the loader's).
	var parsed []*Reflex
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rfx.yaml") {
			continue
		}
		path := filepath.Join(l.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			l.errors = append(l.errors, LoadError{path, err})
			continue
		}
		r, err := Parse(data, path)
		if err != nil {
			l.errors = append(l.errors, LoadError{path, err})
			continue
		}
		parsed = append(parsed, r)
	}
	reflexNames := map[string]bool{}
	for _, r := range parsed {
		reflexNames[r.Name] = true
	}
	known := func(name string) bool {
		if reflexNames[name] {
			return true
		}
		return l.known != nil && l.known(name)
	}
	seen := map[string]string{} // name -> first path, for collision reporting
	for _, r := range parsed {
		if err := Validate(r, known); err != nil {
			l.errors = append(l.errors, LoadError{r.Path, err})
			continue
		}
		if first, dup := seen[r.Name]; dup {
			l.errors = append(l.errors, LoadError{r.Path, &DuplicateError{Name: r.Name, First: first}})
			continue
		}
		seen[r.Name] = r.Path
		l.reflexes = append(l.reflexes, *r)
	}
}

// DuplicateError reports two files claiming the same reflex name.
type DuplicateError struct{ Name, First string }

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("name %q already claimed by %s", e.Name, e.First)
}
