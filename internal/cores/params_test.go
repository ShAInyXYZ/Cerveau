package cores

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A user changes KV from fp8 to bf16 in the panel. The change must reach the
// unit on its next start, and nothing else about the profile may move.
func TestOverridesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cores.d", "vllm-27b-bf16.env")
	if err := SaveOverrides(path, map[string]string{"KV": "bf16", "VISION": "1"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOverrides(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["KV"] != "bf16" || got["VISION"] != "1" || len(got) != 2 {
		t.Fatalf("round trip lost data: %v", got)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `KV="bf16"`) {
		t.Fatalf("file must be a systemd EnvironmentFile, got:\n%s", raw)
	}
}

// Defaults come from the unit; overrides win; a key the profile never named
// still passes through, because the launcher may read it.
func TestEffectiveAppliesOverridesOnDefaults(t *testing.T) {
	eff := Effective(map[string]string{"KV": "fp8", "TP": "4"}, map[string]string{"KV": "bf16", "MAX_PIXELS": "4000000"})
	if eff["KV"] != "bf16" || eff["TP"] != "4" || eff["MAX_PIXELS"] != "4000000" {
		t.Fatalf("effective wrong: %v", eff)
	}
}

// Saving nothing must REMOVE the file, not leave an empty one: the unit would
// otherwise read a stale set after the user reset to the profile.
func TestEmptyOverridesRemoveTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.env")
	if err := SaveOverrides(path, map[string]string{"KV": "bf16"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveOverrides(path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}
	got, err := LoadOverrides(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file must read as empty, got %v %v", got, err)
	}
}

// PORT is where the socket proxy forwards; moving it would make the Core wake
// into a void. Refused before anything touches disk.
func TestPortAndBadKeysAreRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.env")
	for _, bad := range []map[string]string{
		{"PORT": "18099"},
		{"kv": "bf16"},
		{"KV": "bf16\nEVIL=1"},
		{"KV": `a"b`},
	} {
		if err := SaveOverrides(path, bad); err == nil {
			t.Fatalf("expected refusal for %v", bad)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("nothing should have been written")
	}
}
