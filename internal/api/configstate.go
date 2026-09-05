package api

import (
	"fmt"

	"cerveau/internal/config"
)

// ConfigSnapshot gives handlers and runtime callbacks an immutable value. The
// config currently contains only value fields; add deep copies here if a map or
// slice is introduced later.
func (a *API) ConfigSnapshot() config.Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return *a.cfg
}

// updateConfig serializes read-modify-save-publish across every API config
// writer. change is a pure, field-local assignment applied to the candidate and
// then the live value. Publishing individual fields avoids replacing unrelated
// startup-only values. apply publishes runtime defaults only after save succeeds.
// Neither callback may call ConfigSnapshot or another config transaction.
func (a *API) updateConfig(change func(*config.Config), apply func(), requirePath bool) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if requirePath && a.configPath == "" {
		return fmt.Errorf("config path unknown")
	}
	next := *a.cfg
	change(&next)
	if a.configPath != "" {
		if err := config.Save(a.configPath, &next); err != nil {
			return err
		}
	}
	change(a.cfg)
	if apply != nil {
		apply()
	}
	return nil
}

// The legacy workspace callback owns the actual workspace swap and persists
// the same config pointer. Serialize that callback with defaults and pairing so
// its whole-config save cannot race or erase an acknowledged settings update.
// The callback must not re-enter ConfigSnapshot or updateConfig.
func (a *API) changeWorkspace(path string) (string, error) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if err := a.wsChange(path); err != nil {
		return "", err
	}
	return a.cfg.Workspace, nil
}
