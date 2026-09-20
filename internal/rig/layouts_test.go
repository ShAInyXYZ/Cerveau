package rig

import (
	"path/filepath"
	"testing"
)

func TestSavedLayoutsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "rig-layouts.json")
	if got, err := LoadLayouts(path); err != nil || len(got) != 0 {
		t.Fatalf("a missing file is no layouts: %v %v", got, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(SaveLayout(path, "w8a16", SavedLayout{Name: "all four", GPUs: []int{3, 0, 1, 2}, Embed: EmbedPlacement{Device: "cuda", GPUs: []int{4}}}))
	must(SaveLayout(path, "w8a16", SavedLayout{Name: "pack", GPUs: []int{2, 3}, Embed: EmbedPlacement{Device: "cpu"}, GPUUtil: 0.9}))
	// the same name again replaces it — saving as "Pack" twice is one layout
	must(SaveLayout(path, "w8a16", SavedLayout{Name: "Pack", GPUs: []int{0, 1}, Embed: EmbedPlacement{Device: "cpu"}}))
	must(SaveLayout(path, "bf16", SavedLayout{Name: "all four", GPUs: []int{0, 1, 2, 3}, Embed: EmbedPlacement{Device: "cuda", GPUs: []int{4}}}))

	all, err := LoadLayouts(path)
	must(err)
	if len(all["w8a16"]) != 2 || all["w8a16"][0].GPUs[0] != 0 || all["w8a16"][1].Name != "Pack" || all["w8a16"][1].GPUs[0] != 0 {
		t.Fatalf("w8a16: %+v", all["w8a16"])
	}
	must(DeleteLayout(path, "w8a16", "ALL FOUR"))
	must(DeleteLayout(path, "bf16", "all four"))
	all, _ = LoadLayouts(path)
	if len(all["w8a16"]) != 1 || len(all) != 1 {
		t.Fatalf("after delete: %+v", all)
	}
	if err := SaveLayout(path, "w8a16", SavedLayout{Name: "  "}); err == nil {
		t.Fatal("a layout needs a name")
	}
}
