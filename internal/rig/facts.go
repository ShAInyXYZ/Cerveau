package rig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ModelFacts is what a checkpoint on disk says about itself: how big the
// weights are and how much KV cache one token costs. Read from the profile's
// own MODEL path, so the model that is installed can be evaluated with no
// network — nothing here asks huggingface.co anything.
type ModelFacts struct {
	Path        string `json:"path"`
	WeightBytes int64  `json:"weight_bytes"`
	Layers      int    `json:"layers"`
	// AttnLayers keep a KV cache. A hybrid model (Qwen3.5's linear-attention
	// layers) keeps one only on its full-attention layers: counting all 64
	// layers instead of 16 overstates the cache four times.
	AttnLayers   int   `json:"attn_layers"`
	Heads        int   `json:"heads"`
	KVHeads      int   `json:"kv_heads"`
	HeadDim      int   `json:"head_dim"`
	LinearHeads  []int `json:"linear_heads,omitempty"`
	MaxPositions int   `json:"max_positions,omitempty"`
	// Remote: the weights sit on a network mount — every cold start reads them
	// over the wire, and a Core can be woken while the mount is away.
	Remote bool `json:"remote"`
}

// ExpandModelPath resolves what a systemd unit would: %h and ~ are the home.
func ExpandModelPath(p string) string {
	home, _ := os.UserHomeDir()
	p = strings.ReplaceAll(p, "%h", home)
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	return p
}

// ReadFacts reads a checkpoint directory (config.json + weight shards) or a
// single weight file such as a GGUF, which carries no config.json: then only
// the size is known, and the estimate says so.
func ReadFacts(model string) (*ModelFacts, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("this profile does not say which model it loads")
	}
	path := ExpandModelPath(model)
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("the checkpoint is not reachable: %s", path)
	}
	f := &ModelFacts{Path: path, Remote: onNetworkMount(path)}
	if !st.IsDir() {
		f.WeightBytes = st.Size()
		return f, nil
	}
	entries, _ := os.ReadDir(path)
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".safetensors", ".gguf", ".bin", ".pt":
			if info, err := e.Info(); err == nil {
				f.WeightBytes += info.Size()
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(path, "config.json"))
	if err != nil {
		return f, nil
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return f, nil
	}
	// multimodal checkpoints keep the language model under text_config
	cfgRaw := raw
	if t, ok := top["text_config"]; ok {
		cfgRaw = t
	}
	var c struct {
		Layers     int      `json:"num_hidden_layers"`
		Heads      int      `json:"num_attention_heads"`
		KVHeads    int      `json:"num_key_value_heads"`
		HeadDim    int      `json:"head_dim"`
		Hidden     int      `json:"hidden_size"`
		MaxPos     int      `json:"max_position_embeddings"`
		LayerTypes []string `json:"layer_types"`
		FullEvery  int      `json:"full_attention_interval"`
		LinKey     int      `json:"linear_num_key_heads"`
		LinValue   int      `json:"linear_num_value_heads"`
	}
	if json.Unmarshal(cfgRaw, &c) != nil {
		return f, nil
	}
	f.Layers, f.Heads, f.KVHeads, f.HeadDim, f.MaxPositions = c.Layers, c.Heads, c.KVHeads, c.HeadDim, c.MaxPos
	if f.KVHeads == 0 {
		f.KVHeads = f.Heads
	}
	if f.HeadDim == 0 && f.Heads > 0 {
		f.HeadDim = c.Hidden / f.Heads
	}
	f.AttnLayers = f.Layers
	if len(c.LayerTypes) > 0 {
		f.AttnLayers = 0
		for _, t := range c.LayerTypes {
			if t == "full_attention" {
				f.AttnLayers++
			}
		}
	} else if c.FullEvery > 1 {
		f.AttnLayers = f.Layers / c.FullEvery
	}
	for _, n := range []int{c.LinKey, c.LinValue} {
		if n > 0 {
			f.LinearHeads = append(f.LinearHeads, n)
		}
	}
	return f, nil
}

// ReadFactsWithin is ReadFacts that never waits on a drive that has gone away:
// a stat on a dead SMB share can hang for minutes, and a panel request must not.
func ReadFactsWithin(model string, d time.Duration) (*ModelFacts, error) {
	type result struct {
		f   *ModelFacts
		err error
	}
	done := make(chan result, 1)
	go func() {
		f, err := ReadFacts(model)
		done <- result{f, err}
	}()
	select {
	case r := <-done:
		return r.f, r.err
	case <-time.After(d):
		return nil, fmt.Errorf("the checkpoint did not answer in %s — is its drive mounted? %s", d, ExpandModelPath(model))
	}
}

// GroupSizes are the card counts this model can be split across: tensor
// parallelism needs the attention heads to divide evenly, the KV heads to
// divide or be replicated whole, and a hybrid model's linear heads to divide.
// Without head counts, the sizes every common model accepts.
func (f *ModelFacts) GroupSizes(max int) []int {
	out := []int{}
	for n := 1; n <= max; n++ {
		if f.okGroup(n) {
			out = append(out, n)
		}
	}
	return out
}

func (f *ModelFacts) okGroup(n int) bool {
	if f == nil || f.Heads == 0 {
		return n&(n-1) == 0
	}
	if f.Heads%n != 0 || (f.KVHeads%n != 0 && n%f.KVHeads != 0) {
		return false
	}
	for _, h := range f.LinearHeads {
		if h%n != 0 {
			return false
		}
	}
	return true
}

// onNetworkMount finds the mount a path lives on and says whether it is a
// network filesystem. Unknown reads as local.
func onNetworkMount(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	best, fs := "", ""
	for _, line := range strings.Split(string(data), "\n") {
		p := strings.Fields(line)
		if len(p) < 3 {
			continue
		}
		mp := p[1]
		if (path == mp || strings.HasPrefix(path, strings.TrimSuffix(mp, "/")+"/")) && len(mp) > len(best) {
			best, fs = mp, p[2]
		}
	}
	switch fs {
	case "cifs", "smb3", "nfs", "nfs4", "fuse.sshfs", "9p":
		return true
	}
	return false
}
