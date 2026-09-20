package rig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The lab rig's W8A16 checkpoint, as its config.json and shard sizes report it
// (2026-09-20): a hybrid — 64 layers, only 16 of them full attention.
const qwenConfig = `{"architectures":["Qwen3_5ForConditionalGeneration"],"text_config":{
  "num_hidden_layers":64,"num_attention_heads":24,"num_key_value_heads":4,"head_dim":256,"hidden_size":5120,
  "max_position_embeddings":262144,"full_attention_interval":4,"linear_num_key_heads":16,"linear_num_value_heads":48,
  "layer_types":["linear_attention","linear_attention","linear_attention","full_attention"]}}`

// checkpoint writes a config.json and SPARSE weight files of the given sizes:
// the estimate reads sizes, never contents.
func checkpoint(t *testing.T, config string, shards ...int64) string {
	t.Helper()
	dir := t.TempDir()
	if config != "" {
		os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o644)
	}
	for i, size := range shards {
		f, err := os.Create(filepath.Join(dir, "model-0000"+string(rune('1'+i))+".safetensors"))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	os.WriteFile(filepath.Join(dir, "tokenizer.json"), []byte("{}"), 0o644) // not a weight
	return dir
}

func qwenFacts(t *testing.T, bytes int64) *ModelFacts {
	t.Helper()
	// layer_types above is one period of four; the real file lists all 64
	cfg := strings.Replace(qwenConfig, `"layer_types":["linear_attention","linear_attention","linear_attention","full_attention"]`,
		`"layer_types":[`+strings.TrimSuffix(strings.Repeat(`"linear_attention","linear_attention","linear_attention","full_attention",`, 16), ",")+`]`, 1)
	f, err := ReadFacts(checkpoint(t, cfg, bytes/2, bytes-bytes/2))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestReadFactsFromACheckpoint(t *testing.T) {
	f := qwenFacts(t, 31_616_262_888)
	if f.WeightBytes != 31_616_262_888 {
		t.Fatalf("weights = %d: shards must add up and tokenizer.json must not count", f.WeightBytes)
	}
	// 16, not 64: counting the linear-attention layers overstates the KV cache four times
	if f.Layers != 64 || f.AttnLayers != 16 || f.Heads != 24 || f.KVHeads != 4 || f.HeadDim != 256 {
		t.Fatalf("facts: %+v", f)
	}
	// 24 heads divide by 3 and 6, but 4 KV heads and 16 linear heads do not
	if got := f.GroupSizes(8); !reflect.DeepEqual(got, []int{1, 2, 4, 8}) {
		t.Fatalf("group sizes = %v", got)
	}
}

func TestReadFactsSaysWhatIsMissing(t *testing.T) {
	if _, err := ReadFacts(""); err == nil || !strings.Contains(err.Error(), "does not say which model") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ReadFacts("/nonexistent/model"); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("err = %v", err)
	}
	// a bare weight file (GGUF): the size is known, the cost of a token is not
	dir := checkpoint(t, "", 20_000_000_000)
	f, err := ReadFacts(filepath.Join(dir, "model-00001.safetensors"))
	if err != nil || f.WeightBytes != 20_000_000_000 || f.AttnLayers != 0 {
		t.Fatalf("got %+v, %v", f, err)
	}
	if home, _ := os.UserHomeDir(); ExpandModelPath("%h/models/x") != filepath.Join(home, "models/x") {
		t.Fatal("%h is the home directory, as in the unit file")
	}
}

// The W8A16 profile as it runs: four 3090s, 92% of each, fp8 KV, 262K window.
// 31.6 GB ÷ 4 = 7.4 GB of weights a card; one token costs a card 8 KiB.
func TestEstimateTheRunningProfile(t *testing.T) {
	fit := Estimate(qwenFacts(t, 31_616_262_888), labInventory(), nil, []int{0, 1, 2, 3}, 0.92, "fp8", 262144)
	if fit.Verdict != "fits" || len(fit.Cards) != 4 {
		t.Fatalf("fit: %+v", fit)
	}
	c := fit.Cards[0]
	if c.Budget != 22609 || c.Weights != 7537 || c.KVNeeded != 2048 || c.KVRoom != 22609-7537-2560 {
		t.Fatalf("card: %+v", c)
	}
	if fit.MaxWindow < 1_500_000 || fit.MaxWindow > 1_700_000 {
		t.Fatalf("max window = %d, want about 1.6M", fit.MaxWindow)
	}
}

// Packing it onto fewer cards: two still work, one cannot hold the weights.
func TestEstimateFewerCards(t *testing.T) {
	f := qwenFacts(t, 31_616_262_888)
	if two := Estimate(f, labInventory(), nil, []int{2, 3}, 0.92, "fp8", 262144); two.Verdict == "no" {
		t.Fatalf("two cards: %+v", two)
	}
	one := Estimate(f, labInventory(), nil, []int{0}, 0.92, "fp8", 262144)
	if one.Verdict != "no" || !strings.Contains(one.Reasons[0], "the weights need") {
		t.Fatalf("one card: %+v", one)
	}
}

// BF16: 55.6 GB of weights and twice the bytes per token. Four cards at 88%
// hold the full window; asking for bf16 KV on two cards does not.
func TestEstimateTheBF16Profile(t *testing.T) {
	f := qwenFacts(t, 55_600_000_000)
	if fit := Estimate(f, labInventory(), nil, []int{0, 1, 2, 3}, 0.88, "bf16", 262144); fit.Verdict == "no" {
		t.Fatalf("bf16 on four: %+v", fit)
	}
	two := Estimate(f, labInventory(), nil, []int{0, 1}, 0.88, "bf16", 262144)
	if two.Verdict != "no" {
		t.Fatalf("bf16 on two: %+v", two)
	}
}

// When the weights fit but the window does not, say how much context DOES fit:
// that is the number the user can act on.
func TestEstimateNamesTheLargestWindow(t *testing.T) {
	fit := Estimate(qwenFacts(t, 55_600_000_000), labInventory(), nil, []int{0, 1, 2, 3}, 0.75, "bf16", 262144)
	if fit.Verdict != "no" || fit.MaxWindow <= 0 || fit.MaxWindow >= 262144 || !strings.Contains(fit.Reasons[0], "largest window that fits") {
		t.Fatalf("fit: %+v", fit)
	}
}

// vLLM refuses to start when a card's FREE memory is below its share. The
// desktop holds 3.9 GB of the 3060: asking for 92% of it cannot work.
func TestEstimateSeesWhatElseIsOnTheCard(t *testing.T) {
	fit := Estimate(qwenFacts(t, 8_000_000_000), labInventory(), map[int]int{4: 3900}, []int{4}, 0.92, "fp8", 32768)
	if fit.Verdict != "no" || !strings.Contains(fit.Reasons[0], "other things hold 3.8 GB") {
		t.Fatalf("fit: %+v", fit)
	}
	// a 3 GB model asking for 60% leaves the desktop its room
	if ok := Estimate(qwenFacts(t, 3_000_000_000), labInventory(), map[int]int{4: 3900}, []int{4}, 0.6, "fp8", 32768); ok.Verdict == "no" {
		t.Fatalf("at 60%% it fits beside the desktop: %+v", ok)
	}
}

func TestEstimateWithoutFacts(t *testing.T) {
	fit := Estimate(nil, labInventory(), nil, []int{0}, 0.9, "fp8", 32768)
	if fit.Verdict != "unknown" || len(fit.Reasons) != 1 {
		t.Fatalf("fit: %+v", fit)
	}
	// group sizes still come back: the editor needs them to say what is legal
	if !reflect.DeepEqual(fit.GroupSizes, []int{1, 2, 4}) {
		t.Fatalf("group sizes = %v", fit.GroupSizes)
	}
}
