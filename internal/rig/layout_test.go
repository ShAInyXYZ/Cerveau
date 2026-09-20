package rig

import (
	"reflect"
	"strings"
	"testing"

	"cerveau/internal/cores"
)

func planLab(ov map[string]string, gpus []int, embed EmbedPlacement) Plan {
	return PlanLayout(PlanInput{Core: labCore(), Overrides: ov, Inventory: labInventory()}, LayoutRequest{Core: "vllm-27b-w8a16", GPUs: gpus, Embed: embed})
}

var onThe3060 = EmbedPlacement{Device: "cuda", GPUs: []int{4}}

// The user packs the Core onto cards 2 and 3. They had already raised GPU_UTIL
// to 0.92 from the Engine tab. PUT /params REPLACES the overrides file, so the
// plan must hand back 0.92 as well — a plan of placement keys alone would
// silently put the Core back on 0.88.
func TestPlanKeepsWhatTheUserAlreadyChanged(t *testing.T) {
	p := planLab(map[string]string{"GPU_UTIL": "0.92"}, []int{3, 2}, onThe3060)
	if len(p.Problems) != 0 {
		t.Fatalf("problems: %v", p.Problems)
	}
	if p.Core != "vllm-27b-w8a16" {
		t.Fatalf("a plan must say whose it is, got %q", p.Core)
	}
	want := map[string]string{"GPU_UTIL": "0.92", "CUDA_VISIBLE_DEVICES": "2,3", "TP": "2", "CUDA_DEVICE_ORDER": "PCI_BUS_ID"}
	if !reflect.DeepEqual(p.Overrides, want) {
		t.Fatalf("overrides = %v, want %v", p.Overrides, want)
	}
	got := strings.Join(p.Changed, " | ")
	for _, s := range []string{"Core CUDA_VISIBLE_DEVICES 0,1,2,3 → 2,3", "Core TP 4 → 2", "Core CUDA_DEVICE_ORDER — → PCI_BUS_ID", "embedder CUDA_DEVICE_ORDER — → PCI_BUS_ID"} {
		if !strings.Contains(got, s) {
			t.Errorf("changed %q is missing %q", got, s)
		}
	}
}

// Every layout pins the card order, even one that moves nothing: without it an
// index means the card CUDA ranks n-th fastest, not the one the user clicked.
func TestPlanAlwaysPinsTheCardOrder(t *testing.T) {
	p := planLab(nil, []int{0, 1, 2, 3}, onThe3060)
	if p.Overrides["CUDA_DEVICE_ORDER"] != "PCI_BUS_ID" || p.EmbedOverrides["CUDA_DEVICE_ORDER"] != "PCI_BUS_ID" {
		t.Fatalf("order not pinned: %v / %v", p.Overrides, p.EmbedOverrides)
	}
}

func TestPlanRefusesAGroupTheModelCannotSplitAcross(t *testing.T) {
	for _, gpus := range [][]int{{0, 1, 2}, {}, {0, 9}} {
		if p := planLab(nil, gpus, onThe3060); len(p.Problems) == 0 {
			t.Errorf("gpus %v must be refused", gpus)
		}
	}
	if p := planLab(nil, []int{0, 1, 2}, onThe3060); !strings.Contains(p.Problems[0], "1, 2 or 4") {
		t.Fatalf("the refusal must say what works: %v", p.Problems)
	}
	// one card, and the same card named twice, are both one card
	if p := planLab(nil, []int{1, 1}, onThe3060); len(p.Problems) != 0 || p.Overrides["TP"] != "1" {
		t.Fatalf("got %+v", p)
	}
}

// The Core fills each of its cards to GPU_UTIL. On a 24 GB card at 0.88 that
// leaves 2.9 GB: the embedder does not fit beside it, and the plan says so
// before the Core fails to start.
func TestPlanRefusesTheEmbedderOnACardTheCoreFills(t *testing.T) {
	p := planLab(nil, []int{0, 1, 2, 3}, EmbedPlacement{Device: "cuda", GPUs: []int{0}})
	if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "GPU 0 is filled to 88%") {
		t.Fatalf("problems: %v", p.Problems)
	}
	// with the Core told to take half the card, it is a warning, not a refusal
	p = planLab(map[string]string{"GPU_UTIL": "0.5"}, []int{0, 1}, EmbedPlacement{Device: "cuda", GPUs: []int{0}})
	if len(p.Problems) != 0 || len(p.Warnings) != 1 {
		t.Fatalf("problems %v warnings %v", p.Problems, p.Warnings)
	}
}

func TestPlanMovesTheEmbedderToTheCPU(t *testing.T) {
	p := planLab(nil, []int{0, 1, 2, 3}, EmbedPlacement{Device: "cpu"})
	if p.EmbedOverrides["EMBED_DEVICE"] != "cpu" || len(p.Problems) != 0 {
		t.Fatalf("got %+v", p)
	}
	if !strings.Contains(strings.Join(p.Changed, "|"), "embedder EMBED_DEVICE cuda → cpu") {
		t.Fatalf("changed: %v", p.Changed)
	}
}

// A 3090 and the 3060 in one group: legal, but every rank is sized for 12 GB.
func TestPlanWarnsAboutMixedCards(t *testing.T) {
	p := planLab(nil, []int{3, 4}, EmbedPlacement{Device: "cpu"})
	if len(p.Problems) != 0 || len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "12 GB") {
		t.Fatalf("problems %v warnings %v", p.Problems, p.Warnings)
	}
}

// llama.cpp and the single-card vLLM Core declare no placement keys: there is
// nowhere to write, and the plan must not invent keys the launcher never reads.
func TestPlanRefusesAProfileWithoutPlacementKeys(t *testing.T) {
	p := PlanLayout(PlanInput{Core: &cores.Core{ID: "llamacpp", Engine: "llama.cpp"}, Inventory: labInventory()}, LayoutRequest{GPUs: []int{0}})
	if len(p.Problems) != 1 || len(p.Overrides) != 0 {
		t.Fatalf("got %+v", p)
	}
}

// "Allocate more, allocate less": the share of each card is part of the layout.
func TestPlanSetsTheShareOfEachCard(t *testing.T) {
	p := PlanLayout(PlanInput{Core: labCore(), Inventory: labInventory()}, LayoutRequest{GPUs: []int{0, 1, 2, 3}, Embed: onThe3060, GPUUtil: 0.8})
	if p.Overrides["GPU_UTIL"] != "0.8" || p.GPUUtil != 0.8 || p.KV != "fp8" || p.Window != 262144 {
		t.Fatalf("got %+v", p)
	}
	if bad := PlanLayout(PlanInput{Core: labCore(), Inventory: labInventory()}, LayoutRequest{GPUs: []int{0}, Embed: onThe3060, GPUUtil: 1.2}); len(bad.Problems) == 0 {
		t.Fatal("120% of a card must be refused")
	}
}

// With the checkpoint readable, the legal group sizes are the model's own:
// a model with 12 heads and 12 KV heads splits three ways; Qwen3.5 does not.
func TestPlanUsesTheModelsHeadCounts(t *testing.T) {
	three := &ModelFacts{Heads: 12, KVHeads: 12}
	if p := PlanLayout(PlanInput{Core: labCore(), Inventory: labInventory(), Facts: three}, LayoutRequest{GPUs: []int{0, 1, 2}, Embed: onThe3060}); len(p.Problems) != 0 {
		t.Fatalf("problems: %v", p.Problems)
	}
	qwen := &ModelFacts{Heads: 24, KVHeads: 4, LinearHeads: []int{16, 48}}
	if p := PlanLayout(PlanInput{Core: labCore(), Inventory: labInventory(), Facts: qwen}, LayoutRequest{GPUs: []int{0, 1, 2}, Embed: onThe3060}); len(p.Problems) != 1 {
		t.Fatalf("problems: %v", p.Problems)
	}
}

// The embedder's room is what it really holds, when that is known: a small
// embedder fits beside a Core where the default guess would have refused it.
func TestPlanUsesTheEmbeddersMeasuredFootprint(t *testing.T) {
	req := LayoutRequest{GPUs: []int{0, 1, 2, 3}, Embed: EmbedPlacement{Device: "cuda", GPUs: []int{0}}}
	// 12% of a 24 GB card is 2.9 GB: not enough for the 3 GB default…
	if p := PlanLayout(PlanInput{Core: labCore(), Inventory: labInventory()}, req); len(p.Problems) != 1 {
		t.Fatalf("default: %v", p.Problems)
	}
	// …but plenty for an embedder measured at 1.2 GB
	if p := PlanLayout(PlanInput{Core: labCore(), Inventory: labInventory(), EmbedMiB: 1200}, req); len(p.Problems) != 0 || len(p.Warnings) != 1 {
		t.Fatalf("measured: problems %v warnings %v", p.Problems, p.Warnings)
	}
}

func TestBusyAndEmbedderMemory(t *testing.T) {
	use := []CardUse{
		{GPU: 0, Used: 22987, Consumers: []Consumer{{Role: "core", Mem: 22702}, {Role: "other", Mem: 256}}},
		{GPU: 4, Used: 5745, Consumers: []Consumer{{Role: "embedder", Mem: 2282}, {Role: "other", Mem: 104}}},
		{GPU: 1, Used: 0},
	}
	if got := BusyMemory(use); got[0] != 285 || got[4] != 3463 || len(got) != 2 {
		t.Fatalf("busy = %v", got)
	}
	if got := EmbedderMemory(use); got != 2282 {
		t.Fatalf("embedder = %d", got)
	}
}
