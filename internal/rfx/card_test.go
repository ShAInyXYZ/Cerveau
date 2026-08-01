package rfx

import (
	"strings"
	"testing"
)

func TestCheckStepFSNoneBlocksFileTools(t *testing.T) {
	card := Card{FS: []string{"none"}, Network: []string{"none"}}
	for _, tool := range []string{"read", "write", "edit", "grep"} {
		if err := CheckStep(card, tool, map[string]any{"path": "x"}); err == nil || !strings.Contains(err.Error(), "card violation") {
			t.Fatalf("%s not blocked by fs:none: %v", tool, err)
		}
	}
	// bash is not a file tool — fs:none doesn't pretend to sandbox it.
	if err := CheckStep(card, "bash", map[string]any{"command": "ls"}); err != nil {
		t.Fatalf("bash blocked by fs:none (over-enforcement): %v", err)
	}
}

func TestCheckStepNetwork(t *testing.T) {
	allow := Card{Network: []string{"homelab.local", "api.example.com:8080"}}
	if err := CheckStep(allow, "web_fetch", map[string]any{"url": "http://homelab.local/temps"}); err != nil {
		t.Fatalf("allowlisted host blocked: %v", err)
	}
	// host:port pattern matches the hostname basis.
	if err := CheckStep(allow, "web_fetch", map[string]any{"url": "https://api.example.com/v1"}); err != nil {
		t.Fatalf("host:port pattern should match hostname: %v", err)
	}
	err := CheckStep(allow, "web_fetch", map[string]any{"url": "https://evil.example.com/"})
	if err == nil || !strings.Contains(err.Error(), "card violation") {
		t.Fatalf("non-allowlisted host allowed: %v", err)
	}

	allowAny := Card{Network: []string{"any"}}
	if err := CheckStep(allowAny, "web_fetch", map[string]any{"url": "https://anything.example/"}); err != nil {
		t.Fatalf("network:any blocked: %v", err)
	}
	none := Card{Network: []string{"none"}}
	if err := CheckStep(none, "web_fetch", map[string]any{"url": "https://anything.example/"}); err == nil {
		t.Fatal("network:none allowed a fetch")
	}
}

func TestNetworkAllowedDefaultCardIsClosed(t *testing.T) {
	if NetworkAllowed(DefaultCard(), "anything.example") {
		t.Fatal("default card allows network — must be closed by default")
	}
}
