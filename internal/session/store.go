package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Meta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Created   time.Time `json:"created"`
	Workspace string    `json:"workspace,omitempty"`
	// Instant sessions are an ephemeral scratch space: no long-term memory
	// (skipped by the indexer + semantic promotion) and auto-deleted after a TTL.
	Instant  bool      `json:"instant,omitempty"`
	LastSeen time.Time `json:"last_seen,omitempty"` // bumped on activity; TTL counts from here
}

type Store interface {
	List() ([]Meta, error)
	Create(name string) (*Meta, error)
	CreateInWorkspace(name, workspace string) (*Meta, error)
	Rename(id, name string) (*Meta, error)
	Delete(id string) error
	CreateInstant() (*Meta, error)
	Get(id string) (*Meta, error)
	IsInstant(id string) bool
	Touch(id string)
	CountEvents(id string) int
	EventsPath(id string) string
}

type FSStore struct {
	dir       string
	workspace string
}

func NewFSStore(dir string) (*FSStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}
	return &FSStore{dir: dir}, nil
}

func (s *FSStore) SetWorkspace(ws string) { s.workspace = ws }

func (s *FSStore) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(data, &m) == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func (s *FSStore) Create(name string) (*Meta, error) {
	return s.CreateInWorkspace(name, s.workspace)
}

// CreateInWorkspace creates a session stamped with an explicit workspace, so a
// session can be added to a specific project (folder) other than the current one.
func (s *FSStore) CreateInWorkspace(name, workspace string) (*Meta, error) {
	if workspace == "" {
		workspace = s.workspace
	}
	now := time.Now().UTC()
	m := &Meta{
		ID:        now.Format("20060102-150405") + "-" + slug(name),
		Name:      name,
		Created:   now,
		Workspace: workspace,
		LastSeen:  now,
	}
	return s.writeNew(m)
}

// CreateInstant makes an ephemeral scratch session with its OWN isolated scratch
// workspace (under the session dir), so it never touches the user's real projects.
// Flagged Instant (skipped by memory), auto-deleted after a TTL.
func (s *FSStore) CreateInstant() (*Meta, error) {
	now := time.Now().UTC()
	id := now.Format("20060102-150405") + "-instant"
	// the session's own scratch dir doubles as its workspace — isolated + disposable
	scratch := filepath.Join(s.dir, id, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, err
	}
	m := &Meta{
		ID:        id,
		Name:      "Instant " + now.Local().Format("15:04"),
		Created:   now,
		Workspace: scratch,
		Instant:   true,
		LastSeen:  now,
	}
	return s.writeNew(m)
}

func (s *FSStore) writeNew(m *Meta) (*Meta, error) {
	dir := filepath.Join(s.dir, m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), nil, 0o644); err != nil {
		return nil, err
	}
	return m, nil
}

// Rename updates only the session's display Name in meta.json. The ID (the
// immutable primary key that events and memories reference) is untouched, so
// nothing breaks — this is a pure label change.
func (s *FSStore) Rename(id, name string) (*Meta, error) {
	metaPath := filepath.Join(s.dir, id, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	m.Name = name
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(metaPath, out, 0o644); err != nil {
		return nil, err
	}
	return &m, nil
}

// Delete removes a session's OWN data dir (~/.crv/sessions/<id>/: meta.json +
// events.jsonl). It NEVER touches the project workspace or any user files — the
// id is validated to be a plain session id so path traversal can't escape the
// sessions root.
func (s *FSStore) Delete(id string) error {
	if id == "" || strings.ContainsAny(id, "/\\") || id == "." || id == ".." {
		return fmt.Errorf("invalid session id")
	}
	dir := filepath.Join(s.dir, id)
	// belt-and-suspenders: the resolved path must stay inside the sessions root
	if abs, err := filepath.Abs(dir); err != nil || !strings.HasPrefix(abs, mustAbs(s.dir)+string(filepath.Separator)) {
		return fmt.Errorf("refusing to delete outside sessions dir")
	}
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		return fmt.Errorf("session not found")
	}
	return os.RemoveAll(dir)
}

func mustAbs(p string) string { a, _ := filepath.Abs(p); return a }

// CountEvents returns how many events a session's log holds (for the delete preview).
func (s *FSStore) CountEvents(id string) int {
	f, err := os.Open(s.EventsPath(id))
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) > 0 {
			n++
		}
	}
	return n
}

// Get returns a session's meta (nil error only if the file exists + parses).
func (s *FSStore) Get(id string) (*Meta, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, id, "meta.json"))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// IsInstant reports whether a session is an ephemeral instant session (cheap,
// used by the indexer + promotion to decide whether to skip memory writes).
func (s *FSStore) IsInstant(id string) bool {
	m, err := s.Get(id)
	return err == nil && m.Instant
}

// Touch bumps LastSeen so an instant session's TTL counts from its last activity,
// not creation — a session you're actively using won't vanish mid-task.
func (s *FSStore) Touch(id string) {
	m, err := s.Get(id)
	if err != nil {
		return
	}
	m.LastSeen = time.Now().UTC()
	data, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(filepath.Join(s.dir, id, "meta.json"), data, 0o644)
}

// SweepInstant deletes instant sessions whose last activity is older than ttl.
// Returns the ids removed. Falls back to Created if LastSeen is unset.
func (s *FSStore) SweepInstant(ttl time.Duration) []string {
	metas, err := s.List()
	if err != nil {
		return nil
	}
	var removed []string
	cutoff := time.Now().UTC().Add(-ttl)
	for _, m := range metas {
		if !m.Instant {
			continue
		}
		last := m.LastSeen
		if last.IsZero() {
			last = m.Created
		}
		if last.Before(cutoff) {
			if s.Delete(m.ID) == nil {
				removed = append(removed, m.ID)
			}
		}
	}
	return removed
}

func (s *FSStore) EventsPath(id string) string {
	return filepath.Join(s.dir, id, "events.jsonl")
}

func slug(name string) string {
	var b []rune
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b = append(b, r)
		case r >= 'A' && r <= 'Z':
			b = append(b, r+32)
		case r == ' ', r == '-', r == '_':
			b = append(b, '-')
		}
	}
	if len(b) == 0 {
		return "session"
	}
	if len(b) > 40 {
		b = b[:40]
	}
	return string(b)
}
