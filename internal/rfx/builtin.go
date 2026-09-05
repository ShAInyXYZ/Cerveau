package rfx

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"cerveau/rfx/planner"
)

// IgnoredInstalledPack identifies an external copy left untouched because the
// first-party built-in owns its identity. Version is omitted if unreadable.
type IgnoredInstalledPack struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

// LoaderOption changes application-level discovery without changing the
// generic external-pack loader used by validators and independent RFX clients.
type LoaderOption func(*Loader)

func WithBuiltinPlanner() LoaderOption {
	return func(l *Loader) { l.builtinPlanner = true }
}

// BuiltinPlanner derives metadata and its identity from the exact embedded
// manifest and HTML. No release script or installed manifest owns its version.
func BuiltinPlanner() (Pack, error) {
	manifest, err := planner.Files.ReadFile("pack.yaml")
	if err != nil {
		return Pack{}, err
	}
	panel, err := planner.Files.ReadFile("ui/panel.html")
	if err != nil {
		return Pack{}, err
	}
	p, err := ParsePack(manifest, "builtin:planner/pack.yaml")
	if err != nil {
		return Pack{}, err
	}
	if err := ValidatePack(p); err != nil {
		return Pack{}, err
	}
	if p.Pack != "planner" {
		return Pack{}, fmt.Errorf("built-in Planner manifest declares %q", p.Pack)
	}
	if len(panel) > MaxPanelBytes {
		return Pack{}, fmt.Errorf("built-in Planner panel exceeds %d bytes", MaxPanelBytes)
	}
	h := sha256.New()
	for _, file := range []struct {
		name string
		data []byte
	}{{"pack.yaml", manifest}, {"ui/panel.html", panel}} {
		h.Write([]byte(file.name + "\x00"))
		h.Write(file.data)
		h.Write([]byte{0})
	}
	p.Origin = "builtin"
	p.ContentSHA256 = fmt.Sprintf("%x", h.Sum(nil))
	p.Panel = "ui/panel.html"
	p.panelFS = planner.Files
	p.IgnoredInstalled = []IgnoredInstalledPack{}
	return *p, nil
}

// ReadPanel reads the pack's actual source. Built-in panels never fall back to
// installed files; external packs retain their existing live-edit behavior.
func (p Pack) ReadPanel() ([]byte, error) {
	if p.panelFS != nil {
		return fs.ReadFile(p.panelFS, p.Panel)
	}
	return os.ReadFile(p.Panel)
}

func (l *Loader) ignoreInstalledPlanner(path, version string) {
	if version == "" {
		if data, err := os.ReadFile(filepath.Join(path, "pack.yaml")); err == nil {
			if p, err := ParsePack(data, filepath.Join(path, "pack.yaml")); err == nil {
				version = p.Version
			}
		}
	}
	for i := range l.packs {
		if l.packs[i].Pack == "planner" && l.packs[i].Origin == "builtin" {
			l.packs[i].IgnoredInstalled = append(l.packs[i].IgnoredInstalled, IgnoredInstalledPack{Path: path, Version: version})
		}
	}
	l.notices = append(l.notices, fmt.Sprintf("%s: installed Planner ignored; the built-in Planner owns this identity (installed files left untouched)", path))
}
