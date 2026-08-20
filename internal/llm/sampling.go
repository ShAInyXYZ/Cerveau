package llm

import "strings"

// Sampling presets.
//
// These were read once from CRV_TEMP at client construction, so changing the
// temperature meant editing a systemd drop-in and restarting — for a field the
// API accepts on every single request.
//
// The values are measured, not chosen. 0.2 is what every good benchmark run in
// this project used; a "strict" preset of 0.4, taken from Qwen's chat guidance
// rather than from anything tested here, lost visibly on all four benchmark
// projects. Neutral and Creative are deliberately above it for work where the
// answer is not a single correct one — they have NOT been benchmarked, and are
// offered as a choice rather than a recommendation.
type Sampling struct {
	Name string
	Temp float64
	TopP float64
}

var presets = map[string]Sampling{
	"strict":   {"strict", 0.2, 0},
	"neutral":  {"neutral", 0.55, 0.85},
	"creative": {"creative", 0.7, 0.9},
}

// Preset resolves a name. Anything unrecognised falls back to strict: an
// unknown name is a bug or a typo, and inventing a temperature from it would
// silently change how the model writes code.
func Preset(name string) Sampling {
	if p, ok := presets[strings.ToLower(strings.TrimSpace(name))]; ok {
		return p
	}
	return presets["strict"]
}

// PresetNames lists the presets in the order a UI should show them: tightest
// first, since that is the default and the one that writes code.
func PresetNames() []string { return []string{"strict", "neutral", "creative"} }

// samplingFor applies a per-request override, falling back to the client's
// session default. This is what lets one turn run hotter without changing the
// setting everything else uses.
func (c *Client) samplingFor(override string) Sampling {
	if strings.TrimSpace(override) != "" {
		return Preset(override)
	}
	return c.sampling
}

// SetSampling changes the session default, live.
func (c *Client) SetSampling(name string) { c.sampling = Preset(name) }

// SamplingName reports the current session default.
func (c *Client) SamplingName() string { return c.sampling.Name }
