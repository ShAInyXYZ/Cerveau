package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const ProjectName = "cerveau"

type Endpoints struct {
	Model     string `json:"model"`
	Embedder  string `json:"embedder"`
	Typesense string `json:"typesense"`
}

type Config struct {
	Project          string    `json:"project"`
	Addr             string    `json:"addr"`
	Workspace        string    `json:"workspace"`
	SessionsDir      string    `json:"sessions_dir"`
	ModelCtx         int       `json:"model_ctx"`
	TypesenseKey     string    `json:"typesense_key,omitempty"`
	TypesenseManaged bool      `json:"typesense_managed,omitempty"`
	Endpoints        Endpoints `json:"endpoints"`
}

func Default() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Project: ProjectName,
		// Bind to LOCALHOST only. The API is unauthenticated and can execute bash
		// (autopilot), so a 0.0.0.0 bind would expose remote code execution to the
		// whole LAN. Set Addr to ":7700" in config explicitly to expose it — and
		// only behind your own auth/tunnel.
		Addr:        "127.0.0.1:7700",
		Workspace:   ".",
		SessionsDir: filepath.Join(home, ".crv", "sessions"),
		ModelCtx:    32768,
		Endpoints: Endpoints{
			Model:     "http://localhost:8080",
			Embedder:  "http://localhost:8081",
			Typesense: "http://localhost:8108",
		},
	}
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cerveau", "config.json")
}

func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Project != ProjectName {
		return nil, fmt.Errorf("config project %q is not %q — refusing to load a foreign config", cfg.Project, ProjectName)
	}
	applyEnv(cfg)
	return cfg, nil
}

func LoadOrCreate(path string) (*Config, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	cfg = Default()
	if err := Save(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("CRV_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := os.Getenv("CRV_MODEL_URL"); v != "" {
		cfg.Endpoints.Model = v
	}
	if v := os.Getenv("CRV_EMBEDDER_URL"); v != "" {
		cfg.Endpoints.Embedder = v
	}
	if v := os.Getenv("CRV_TYPESENSE_URL"); v != "" {
		cfg.Endpoints.Typesense = v
	}
	if v := os.Getenv("CRV_SESSIONS_DIR"); v != "" {
		cfg.SessionsDir = v
	}
}
