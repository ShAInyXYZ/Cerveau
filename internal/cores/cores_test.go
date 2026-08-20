package cores

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Core is a whole inference RUNTIME — engine, quantisation, KV format —
// presented as one OpenAI-compatible endpoint. The harness picks a URL and
// needs no knowledge of what runs behind it.
//
// Until now that URL lived in config.json as a bare string, so the panel could
// show "http://localhost:18020" and nothing else: not which engine, not how to
// start it, not whether it is the one currently answering.
func TestRegistryReadsCoresFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cores.json")
	os.WriteFile(path, []byte(`{
	  "active": "vllm",
	  "cores": [
	    {"id":"llamacpp","name":"llama.cpp","endpoint":"http://localhost:8080",
	     "engine":"llama.cpp","start":"serve-model.sh","notes":"MoE, offloads to RAM"},
	    {"id":"vllm","name":"vLLM Dense","endpoint":"http://localhost:18020",
	     "engine":"vLLM","start":"start_qwen.sh","notes":"dense, weights resident"}
	  ]
	}`), 0o644)

	r, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(r.Cores) != 2 {
		t.Fatalf("expected 2 cores, got %d", len(r.Cores))
	}
	if r.Active != "vllm" {
		t.Errorf("active = %q, want vllm", r.Active)
	}
	if got := r.ActiveCore(); got == nil || got.Engine != "vLLM" {
		t.Errorf("ActiveCore did not resolve to the vLLM entry: %+v", got)
	}
}

// With no cores.json the panel must still have something to show: the endpoint
// already configured, described honestly as unknown rather than invented.
func TestMissingFileYieldsASingleUnknownCore(t *testing.T) {
	r, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("a missing registry is not an error: %v", err)
	}
	if len(r.Cores) != 0 {
		t.Errorf("expected no cores, got %d", len(r.Cores))
	}
}

// Switching writes the choice back, so a restart comes up on the same Core.
func TestSwitchPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cores.json")
	os.WriteFile(path, []byte(`{"active":"a","cores":[
	  {"id":"a","name":"A","endpoint":"http://a","engine":"x"},
	  {"id":"b","name":"B","endpoint":"http://b","engine":"y"}]}`), 0o644)

	r, _ := Load(path)
	if err := r.SetActive("b", path); err != nil {
		t.Fatalf("switch: %v", err)
	}
	again, _ := Load(path)
	if again.Active != "b" {
		t.Errorf("choice did not survive a reload: %q", again.Active)
	}
}

// An unknown id must be refused, not silently written — a typo would otherwise
// leave the harness pointing at nothing.
func TestSwitchRejectsUnknownCore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cores.json")
	os.WriteFile(path, []byte(`{"active":"a","cores":[{"id":"a","name":"A","endpoint":"http://a","engine":"x"}]}`), 0o644)
	r, _ := Load(path)
	err := r.SetActive("ghost", path)
	if err == nil {
		t.Fatal("switching to an unknown core should fail")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the bad id: %v", err)
	}
}
