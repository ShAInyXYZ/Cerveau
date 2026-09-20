package rig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Saved layouts — "Save as". A profile is a systemd unit with its own port, so
// a drawn placement cannot become a NEW profile from the panel. What it can
// become is a named layout kept beside the profile: try "all four", "pack on
// 2 and 3", "leave the 3060 alone", and load whichever again later. Loading
// one only fills the editor; Save is still what writes the profile.

type SavedLayout struct {
	Name    string         `json:"name"`
	GPUs    []int          `json:"gpus"`
	Embed   EmbedPlacement `json:"embed"`
	GPUUtil float64        `json:"gpu_util,omitempty"`
}

func LayoutsPath() string {
	if p := os.Getenv("CRV_RIG_LAYOUTS"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cerveau", "rig-layouts.json")
}

// LoadLayouts reads every profile's layouts. A missing file is no layouts.
func LoadLayouts(path string) (map[string][]SavedLayout, error) {
	all := map[string][]SavedLayout{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return all, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("rig-layouts.json: %w", err)
	}
	return all, nil
}

// maxLayouts bounds what one profile keeps: the file is written from the panel.
const maxLayouts = 24

// SaveLayout adds a layout to a profile, replacing one of the same name.
func SaveLayout(path, core string, l SavedLayout) error {
	l.Name = strings.TrimSpace(l.Name)
	if l.Name == "" || len(l.Name) > 60 {
		return fmt.Errorf("a layout needs a name of at most 60 characters")
	}
	if strings.TrimSpace(core) == "" {
		return fmt.Errorf("a layout belongs to a profile")
	}
	all, err := LoadLayouts(path)
	if err != nil {
		return err
	}
	l.GPUs = uniqueSorted(l.GPUs)
	kept := []SavedLayout{}
	for _, x := range all[core] {
		if !strings.EqualFold(x.Name, l.Name) {
			kept = append(kept, x)
		}
	}
	if len(kept) >= maxLayouts {
		return fmt.Errorf("this profile already keeps %d layouts — forget one first", maxLayouts)
	}
	all[core] = append(kept, l)
	return writeLayouts(path, all)
}

func DeleteLayout(path, core, name string) error {
	all, err := LoadLayouts(path)
	if err != nil {
		return err
	}
	kept := []SavedLayout{}
	for _, x := range all[core] {
		if !strings.EqualFold(x.Name, name) {
			kept = append(kept, x)
		}
	}
	if len(kept) == 0 {
		delete(all, core)
	} else {
		all[core] = kept
	}
	return writeLayouts(path, all)
}

func writeLayouts(path string, all map[string][]SavedLayout) error {
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
