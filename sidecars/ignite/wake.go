package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"cerveau/internal/config"
	"cerveau/internal/cores"
)

type wakeTarget struct {
	id, endpoint, unit string
	alternatives       []string
}

func (t wakeTarget) units() []string {
	units := []string{"cerveau.service", "cerveau-embed.service"}
	if t.unit != "" {
		units = append(units, t.unit)
	}
	return units
}

var serviceUnit = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@:-]*\.service$`)

func configuredTarget() (wakeTarget, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return wakeTarget{}, fmt.Errorf("read Cerveau config: %w", err)
	}
	reg, err := cores.Load(cores.DefaultPath())
	if err != nil {
		return wakeTarget{}, err
	}
	return selectTarget(cfg.Endpoints.Model, reg)
}

// The endpoint used by Cerveau is authoritative, not a stale registry.active
// label. An unmanaged/remote endpoint needs no local model unit; never guess one
// or execute a registry's human-readable Start shell command.
func selectTarget(endpoint string, reg *cores.Registry) (wakeTarget, error) {
	t := wakeTarget{endpoint: endpoint, id: "unmanaged"}
	if reg == nil {
		return t, fmt.Errorf("Core registry unavailable")
	}
	matches := 0
	for _, core := range reg.Cores {
		if core.Unit != "" && (len(core.Unit) > 255 || !serviceUnit.MatchString(core.Unit)) {
			return t, fmt.Errorf("invalid Core service unit")
		}
		if core.Endpoint == endpoint {
			matches++
			t.id, t.unit = core.ID, core.Unit
		}
	}
	if endpoint == "" || matches > 1 {
		return t, fmt.Errorf("missing or ambiguous selected Core endpoint")
	}
	seen := map[string]bool{t.unit: true}
	for _, core := range reg.Cores {
		if core.Unit != "" && !seen[core.Unit] {
			t.alternatives = append(t.alternatives, core.Unit)
			seen[core.Unit] = true
		}
	}
	return t, nil
}

type wakeManager struct {
	mu      sync.Mutex
	load    func() (wakeTarget, error)
	command func(context.Context, ...string) (string, error)
	now     func() time.Time
	last    time.Time
	key     string
	lastErr error
}

func (m *wakeManager) ensure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := m.load()
	if err != nil {
		return err
	}
	key := target.endpoint + "\x00" + target.unit
	if m.key == key && !m.last.IsZero() && m.now().Sub(m.last) < 5*time.Second {
		return m.lastErr
	}
	m.key = key
	m.lastErr = m.start(ctx, target)
	m.last = m.now()
	if ctx.Err() != nil {
		// A disconnected caller must not make healthy retries inherit its
		// cancellation for the entire throttle interval.
		m.last = time.Time{}
	}
	return m.lastErr
}

func (m *wakeManager) start(ctx context.Context, target wakeTarget) error {
	if target.unit != "" {
		for _, unit := range target.alternatives {
			out, commandErr := m.command(ctx, "show", "--property=Id,Names,LoadState,ActiveState", "--no-pager", unit)
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := alternateCoreInactive(unit, out, commandErr); err != nil {
				return err
			}
		}
	}
	// Refuse selection changes observed during the service-state check. Starts
	// are async systemd jobs, not a synchronous wait for GPU weights to load.
	latest, err := m.load()
	if err != nil || latest.endpoint != target.endpoint || latest.unit != target.unit {
		return fmt.Errorf("selected Core changed; retry after the switch settles")
	}
	// One enqueue request avoids a selection change between separate harness,
	// embedder and model start calls. The watchdog remains responsible for
	// stopping/switching profiles; Ignite never issues stop or restart.
	args := append([]string{"start", "--no-block"}, target.units()...)
	if _, err := m.command(ctx, args...); err != nil {
		return fmt.Errorf("queue configured Cerveau stack: %w", err)
	}
	return nil
}

func alternateCoreInactive(unit, output string, commandErr error) error {
	properties := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if _, duplicate := properties[key]; duplicate {
			return fmt.Errorf("cannot verify alternate Core %s: duplicate service metadata", unit)
		}
		properties[key] = value
	}
	identityMatches := properties["Id"] == unit
	for _, name := range strings.Fields(properties["Names"]) {
		identityMatches = identityMatches || name == unit
	}
	if !identityMatches {
		return fmt.Errorf("cannot verify alternate Core %s: service identity unavailable", unit)
	}
	state := properties["ActiveState"]
	if state != "inactive" && state != "failed" {
		return fmt.Errorf("alternate Core %s is active, switching or unavailable (%q); refusing a competing model start", unit, state)
	}
	// Optional profiles can be registered but not installed. Explicit complete
	// not-found/inactive metadata proves absence even if systemctl exits nonzero.
	// Bus failures, incomplete output and unknown load states do not prove this.
	if properties["LoadState"] == "not-found" && state == "inactive" {
		return nil
	}
	if commandErr != nil || properties["LoadState"] != "loaded" {
		return fmt.Errorf("cannot verify alternate Core %s: service state unavailable", unit)
	}
	return nil
}
