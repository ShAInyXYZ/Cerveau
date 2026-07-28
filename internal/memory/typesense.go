package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"cerveau/internal/config"
)

const (
	defaultTypesenseURL = "http://localhost:8108"
	managedPortStart    = 8188
)

type TypesenseEndpoint struct {
	URL     string
	Key     string
	Managed bool
	cmd     *exec.Cmd
}

func (e *TypesenseEndpoint) Close() {
	if e.cmd != nil && e.cmd.Process != nil {
		e.cmd.Process.Kill()
		e.cmd.Wait()
	}
}

func EnsureTypesense(cfg *config.Config, configPath string) (*TypesenseEndpoint, error) {
	if !cfg.TypesenseManaged && cfg.Endpoints.Typesense != defaultTypesenseURL {
		if healthy(cfg.Endpoints.Typesense, cfg.TypesenseKey) {
			return &TypesenseEndpoint{URL: cfg.Endpoints.Typesense, Key: cfg.TypesenseKey}, nil
		}
		return nil, fmt.Errorf("custom typesense %s unreachable", cfg.Endpoints.Typesense)
	}
	return bootstrap(cfg, configPath)
}

func bootstrap(cfg *config.Config, configPath string) (*TypesenseEndpoint, error) {
	if cfg.TypesenseManaged && healthy(cfg.Endpoints.Typesense, cfg.TypesenseKey) {
		slog.Info("typesense: reusing running managed instance", "url", cfg.Endpoints.Typesense)
		return &TypesenseEndpoint{URL: cfg.Endpoints.Typesense, Key: cfg.TypesenseKey, Managed: true}, nil
	}
	home, _ := os.UserHomeDir()
	crvDir := filepath.Join(home, ".crv")
	binPath := filepath.Join(crvDir, "bin", "typesense-server")
	dataDir := filepath.Join(crvDir, "typesense-data")

	if _, err := os.Stat(binPath); err != nil {
		slog.Info("typesense: downloading managed binary")
		if err := Install(binPath); err != nil {
			return nil, fmt.Errorf("typesense install: %w", err)
		}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	key := cfg.TypesenseKey
	if key == "" {
		var err error
		key, err = genKey()
		if err != nil {
			return nil, err
		}
	}
	port := findFreePort(managedPortStart)
	if cfg.TypesenseManaged {
		if p := portOf(cfg.Endpoints.Typesense); p != 0 && portFree(p) {
			port = p
		}
	}
	cmd := exec.Command(binPath,
		"--data-dir", dataDir,
		"--api-key", key,
		"--api-port", fmt.Sprint(port),
		"--enable-cors",
	)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("typesense spawn: %w", err)
	}
	url := fmt.Sprintf("http://localhost:%d", port)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if healthy(url, key) {
			cfg.TypesenseKey = key
			cfg.TypesenseManaged = true
			cfg.Endpoints.Typesense = url
			if err := config.Save(configPath, cfg); err != nil {
				slog.Warn("typesense: could not persist key to config", "err", err)
			}
			slog.Info("typesense: managed instance online", "url", url)
			return &TypesenseEndpoint{URL: url, Key: key, Managed: true, cmd: cmd}, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	return nil, fmt.Errorf("typesense failed to become healthy on %s", url)
}

func portOf(rawurl string) int {
	u, err := url.Parse(rawurl)
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		return 0
	}
	return p
}

func EndpointHealthy(base string) bool {
	return healthy(base, "")
}

func healthy(base, key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	if err != nil {
		return false
	}
	if key != "" {
		req.Header.Set("x-typesense-api-key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	var out struct {
		OK bool `json:"ok"`
	}
	return json.NewDecoder(resp.Body).Decode(&out) == nil && out.OK
}

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func findFreePort(start int) int {
	for p := start; p < start+100; p++ {
		if portFree(p) {
			return p
		}
	}
	return start
}
