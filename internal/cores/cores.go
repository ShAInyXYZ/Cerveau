// Package cores is the registry of Brain Cores.
//
// A Core is a whole inference RUNTIME — engine, quantisation, KV format,
// serving strategy — presented as one OpenAI-compatible endpoint. The harness
// picks a URL and needs no knowledge of what runs behind it, which is exactly
// what lets llama.cpp and vLLM coexist without the loop knowing either exists.
//
// The endpoint alone was never enough for the panel: a bare
// "http://localhost:18020" cannot tell a user which engine is answering, how to
// start the other one, or what the trade between them is. That is what this
// file adds — description, not capability.
package cores

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Core struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Engine   string `json:"engine"`
	// Start is the command that brings this Core up. Shown to the user, never
	// executed: a harness that can start engines can also stop the one it is
	// talking to, and the failure mode is a machine with no model at all.
	Start string `json:"start,omitempty"`
	Notes string `json:"notes,omitempty"`
	Model string `json:"model,omitempty"`
	Ctx   int    `json:"ctx,omitempty"`
}

type Registry struct {
	Active string `json:"active"`
	Cores  []Core `json:"cores"`
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cerveau", "cores.json")
}

// Load reads the registry. A missing file is NOT an error: Cores are optional
// description on top of an endpoint that already works, so a fresh install runs
// exactly as before with an empty registry.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{}, nil
		}
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("cores.json: %w", err)
	}
	return &r, nil
}

func (r *Registry) ActiveCore() *Core {
	for i := range r.Cores {
		if r.Cores[i].ID == r.Active {
			return &r.Cores[i]
		}
	}
	return nil
}

// ByEndpoint finds the Core currently being talked to, so the panel can mark it
// active even when cores.json has never been written.
func (r *Registry) ByEndpoint(url string) *Core {
	for i := range r.Cores {
		if r.Cores[i].Endpoint == url {
			return &r.Cores[i]
		}
	}
	return nil
}

// SetActive records the choice and persists it. An unknown id is refused rather
// than written: a typo would otherwise point the harness at nothing, and the
// symptom (every turn failing to connect) is far from the cause.
func (r *Registry) SetActive(id, path string) error {
	found := false
	for i := range r.Cores {
		if r.Cores[i].ID == id {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no core with id %q", id)
	}
	r.Active = id
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
