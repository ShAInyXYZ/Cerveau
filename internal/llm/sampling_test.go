package llm

import "testing"

// Temperature was read once, from the environment, at client construction — so
// changing it meant editing a systemd drop-in and restarting the harness. It is
// a PER-REQUEST field in the API; nothing about it needs a restart.
func TestPresetsResolveToConfiguredValues(t *testing.T) {
	cases := map[string]struct{ temp, topP float64 }{
		"default":  {Unset, Unset},
		"strict":   {0.2, 0},
		"neutral":  {0.55, 0.85},
		"creative": {0.7, 0.9},
	}
	for name, want := range cases {
		got := Preset(name)
		if got.Temp != want.temp || got.TopP != want.topP {
			t.Errorf("%s = %v/%v, want %v/%v", name, got.Temp, got.TopP, want.temp, want.topP)
		}
	}
}

// An unknown or empty name must fall back to the safe default rather than
// sending whatever zero values happen to be in the struct.
func TestUnknownPresetFallsBackToCoreDefaults(t *testing.T) {
	for _, name := range []string{"", "  ", "wild", "0.9"} {
		if got := Preset(name); got != Preset("default") {
			t.Errorf("Preset(%q) = %+v, want Core defaults", name, got)
		}
	}
}

// The per-request override is what makes a chat-bar control possible: one turn
// at a different setting, without touching the session default.
func TestRequestOverrideBeatsTheClientDefault(t *testing.T) {
	c := &Client{sampling: Preset("strict")}
	if got := c.samplingFor("creative"); got.Temp != 0.7 {
		t.Errorf("override ignored: got %v, want 0.7", got.Temp)
	}
	if got := c.samplingFor(""); got.Temp != 0.2 {
		t.Errorf("empty override should keep the client default: got %v", got.Temp)
	}
}
