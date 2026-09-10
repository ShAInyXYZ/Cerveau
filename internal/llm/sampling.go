package llm

import "strings"

// Sampling presets.
//
// These were read once from CRV_TEMP at client construction, so changing the
// temperature meant editing a systemd drop-in and restarting — for a field the
// API accepts on every single request.
//
// Default defers to the Core. Explicit presets are optional overrides, not
// proven quality rankings: historical comparisons changed both sampling and
// harness code, so they cannot isolate sampling's effect.
type Sampling struct {
	Name string
	Temp float64
	TopP float64
}

// Unset means "send no sampling field at all", so the Core applies the
// configured sampling defaults for the served model. Effective values depend
// on the Core's configuration. This is not temperature 0 (greedy decoding).
const Unset = -1

var presets = map[string]Sampling{
	// "default" hands sampling back to the model. The tuned presets below
	// override it; this one gets out of the way.
	"default":  {"default", Unset, Unset},
	"strict":   {"strict", 0.2, 0},
	"neutral":  {"neutral", 0.55, 0.85},
	"creative": {"creative", 0.7, 0.9},
}

// Preset resolves a name. Empty or unknown names defer to the Core instead of
// silently imposing a harness-specific temperature. The settings API rejects
// unknown names before persistence.
func Preset(name string) Sampling {
	if p, ok := presets[strings.ToLower(strings.TrimSpace(name))]; ok {
		return p
	}
	return presets["default"]
}

// PresetNames lists Core defaults first, followed by optional overrides.
func PresetNames() []string { return []string{"default", "strict", "neutral", "creative"} }

// samplingFor applies a per-request override, falling back to the client's
// session default. This is what lets one turn run hotter without changing the
// setting everything else uses.
func (c *Client) samplingFor(override string) Sampling {
	if strings.TrimSpace(override) != "" {
		return Preset(override)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sampling
}

// SetSampling changes the session default, live.
func (c *Client) SetSampling(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sampling = Preset(name)
}

// SamplingName reports the current session default.
func (c *Client) SamplingName() string { c.mu.RLock(); defer c.mu.RUnlock(); return c.sampling.Name }
