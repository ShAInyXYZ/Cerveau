package rig

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"cerveau/internal/cores"
)

// The shape of real nvidia-smi output on a five-card rig — four 3090s and a
// 3060 — with the card UUIDs replaced: a UUID names one physical card.
const labGPUs = `0, GPU-aaaaaaaa-0000-4000-8000-000000000000, NVIDIA GeForce RTX 3090, 24576, 4, 16, 250.00, 8.6
1, GPU-bbbbbbbb-0000-4000-8000-000000000001, NVIDIA GeForce RTX 3090, 24576, 4, 16, 250.00, 8.6
2, GPU-cccccccc-0000-4000-8000-000000000002, NVIDIA GeForce RTX 3090, 24576, 4, 16, 250.00, 8.6
3, GPU-dddddddd-0000-4000-8000-000000000003, NVIDIA GeForce RTX 3090, 24576, 4, 16, 250.00, 8.6
4, GPU-eeeeeeee-0000-4000-8000-000000000004, NVIDIA GeForce RTX 3060, 12288, 4, 16, 190.00, 8.6
`

// The header carries ANSI underline codes, and affinity columns trail each row.
const labTopo = "\t\x1b[4mGPU0\tGPU1\tGPU2\tGPU3\tGPU4\tCPU Affinity\tNUMA Affinity\tGPU NUMA ID\x1b[0m\n" +
	"GPU0\t X \tNODE\tNODE\tNODE\tNODE\t0-31\t0\t\tN/A\n" +
	"GPU1\tNODE\t X \tNODE\tNODE\tNODE\t0-31\t0\t\tN/A\n" +
	"GPU2\tNODE\tNODE\t X \tNODE\tNODE\t0-31\t0\t\tN/A\n" +
	"GPU3\tNODE\tNODE\tNODE\t X \tNODE\t0-31\t0\t\tN/A\n" +
	"GPU4\tNODE\tNODE\tNODE\tNODE\t X \t0-31\t0\t\tN/A\n" +
	"\nLegend:\n\n  X    = Self\n"

func labInventory() Inventory { return Inventory{GPUs: parseGPUs(labGPUs)} }

// The canvas draws one card per entry, so a dropped or merged line is a
// missing GPU on screen — the 2026-09-04 bug, where five cards showed as one.
func TestParseGPUsSeesEveryCard(t *testing.T) {
	gpus := parseGPUs(labGPUs)
	if len(gpus) != 5 {
		t.Fatalf("want 5 cards, got %d", len(gpus))
	}
	g := gpus[4]
	if g.Index != 4 || g.Name != "NVIDIA GeForce RTX 3060" || g.MemTotal != 12288 ||
		g.PCIeGen != 4 || g.PCIeWidth != 16 || g.PowerLimit != 190 || g.ComputeCap != "8.6" {
		t.Fatalf("3060 read wrong: %+v", g)
	}
	if !strings.HasPrefix(gpus[0].UUID, "GPU-aaaaaaaa") {
		t.Fatalf("uuid lost: %q", gpus[0].UUID)
	}
}

// A card that does not report a field says "[N/A]"; that must read as absent,
// never as a parse failure that drops the whole card.
func TestParseGPUsToleratesMissingFields(t *testing.T) {
	gpus := parseGPUs("0, GPU-aa, Tesla Old, 16384, [N/A], [N/A], [N/A], [N/A]\n")
	if len(gpus) != 1 || gpus[0].MemTotal != 16384 || gpus[0].PCIeGen != 0 || gpus[0].ComputeCap != "" {
		t.Fatalf("got %+v", gpus)
	}
}

func TestParseTopoReadsTheGPUBlockOnly(t *testing.T) {
	links := parseTopo(labTopo, 5)
	if len(links) != 5 {
		t.Fatalf("want a 5×5 matrix, got %v", links)
	}
	if links[0][0] != "X" || links[0][4] != "NODE" || links[4][4] != "X" || len(links[2]) != 5 {
		t.Fatalf("matrix wrong: %v", links)
	}
	if hasNVLink(links) {
		t.Fatal("this rig has no NVLink")
	}
	if !hasNVLink([][]string{{"X", "NV4"}, {"NV4", "X"}}) {
		t.Fatal("NV4 is NVLink")
	}
}

// A truncated matrix is worse than none: the canvas would draw links for some
// cards and silence for others.
func TestParseTopoRefusesAPartialMatrix(t *testing.T) {
	if links := parseTopo("GPU0\t X \tNODE\n", 2); links != nil {
		t.Fatalf("want nil, got %v", links)
	}
}

func TestUnitOfPicksTheInnermostUnit(t *testing.T) {
	cases := map[string]string{
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/crv-core-vllm-27b-w8a16.service\n": "crv-core-vllm-27b-w8a16.service",
		"0::/system.slice/docker-0123456789ab.scope\n":                                                 "docker-0123456789ab.scope",
		"0::/user.slice/user-1000.slice/session-2.scope\n":                                             "session-2.scope",
		"0::/\n": "",
		"":       "",
	}
	for in, want := range cases {
		if got := unitOf(in); got != want {
			t.Errorf("unitOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// What the rig actually looked like with the Core parked: a docker container
// holding a little memory on every card. It must read as "other", on all five.
func TestObservedAttributesMemoryByUnit(t *testing.T) {
	reg := &cores.Registry{Cores: []cores.Core{{ID: "vllm-27b-w8a16", Unit: "crv-core-vllm-27b-w8a16.service"}}}
	const u0, u4 = "GPU-aaaaaaaa-0000-4000-8000-000000000000", "GPU-eeeeeeee-0000-4000-8000-000000000004"
	restore := stub(map[string]string{
		"--query-gpu=uuid,memory.used": u0 + ", 8100\n" + u4 + ", 1600\n",
		"--query-compute-apps": u0 + ", 100, 7800, VLLM::Worker_TP0\n" +
			u0 + ", 300, 256, python3\n" +
			u4 + ", 200, 1500, python3\n",
	}, map[int]string{
		100: "0::/user.slice/user-1000.slice/user@1000.service/app.slice/crv-core-vllm-27b-w8a16.service\n",
		200: "0::/user.slice/user-1000.slice/user@1000.service/app.slice/cerveau-embed.service\n",
		300: "0::/system.slice/docker-0123456789ab.scope\n",
	})
	defer restore()

	use := Observed(labInventory(), reg)
	if len(use) != 5 {
		t.Fatalf("one entry per card, got %d", len(use))
	}
	if use[0].Used != 8100 || len(use[0].Consumers) != 2 {
		t.Fatalf("card 0: %+v", use[0])
	}
	if c := use[0].Consumers[0]; c.Role != "core" || c.Core != "vllm-27b-w8a16" || c.Mem != 7800 {
		t.Fatalf("core worker: %+v", c)
	}
	if c := use[0].Consumers[1]; c.Role != "other" || c.Unit != "docker-0123456789ab.scope" {
		t.Fatalf("docker: %+v", c)
	}
	if c := use[4].Consumers[0]; c.Role != "embedder" || c.Mem != 1500 {
		t.Fatalf("embedder: %+v", c)
	}
	// an idle card is an empty list, not null — the panel maps over it
	if use[2].Consumers == nil || len(use[2].Consumers) != 0 {
		t.Fatalf("card 2: %+v", use[2])
	}
}

func TestObservedOnAMachineWithoutGPUs(t *testing.T) {
	if use := Observed(Inventory{}, &cores.Registry{}); use != nil {
		t.Fatalf("want nil, got %v", use)
	}
}

// The W8A16 profile as install.sh writes it: four 3090s for the Core, the
// 3060 for the embedder.
func labCore() *cores.Core {
	return &cores.Core{
		ID: "vllm-27b-w8a16", Name: "vLLM · W8A16 · TP=4", Engine: "vLLM", Ctx: 262144,
		Params: map[string]string{"CUDA_VISIBLE_DEVICES": "0,1,2,3", "TP": "4", "GPU_UTIL": "0.88", "KV": "fp8", "MAX_LEN": "262144"},
		Embed:  map[string]string{"EMBED_DEVICE": "cuda", "CUDA_VISIBLE_DEVICES": "4", "EMBED_THREADS": "4"},
	}
}

func TestDeclaredReadsTheProfile(t *testing.T) {
	p := Declared(labCore(), nil, nil, labInventory())
	if !reflect.DeepEqual(p.GPUs, []int{0, 1, 2, 3}) || p.TP != 4 || p.GPUUtil != 0.88 || p.KV != "fp8" || p.MaxLen != 262144 {
		t.Fatalf("core placement: %+v", p)
	}
	if p.Embed.Device != "cuda" || !reflect.DeepEqual(p.Embed.GPUs, []int{4}) {
		t.Fatalf("embed placement: %+v", p.Embed)
	}
	if !p.Editable || p.Overridden {
		t.Fatalf("editable=%v overridden=%v", p.Editable, p.Overridden)
	}
}

// The user packs the Core onto cards 2 and 3 and leaves 0 and 1 alone. The
// override wins, and the placement says it has left the profile.
func TestDeclaredAppliesALayoutOverride(t *testing.T) {
	p := Declared(labCore(), map[string]string{"CUDA_VISIBLE_DEVICES": "2,3", "TP": "2"}, nil, labInventory())
	if !reflect.DeepEqual(p.GPUs, []int{2, 3}) || p.TP != 2 || !p.Overridden {
		t.Fatalf("got %+v", p)
	}
	// a KV change is a parameter change, not a move
	if Declared(labCore(), map[string]string{"KV": "bf16"}, nil, labInventory()).Overridden {
		t.Fatal("KV alone must not read as a moved layout")
	}
}

// nvidia-smi counts cards in bus order; CUDA counts them fastest-first unless
// told otherwise. Neither profile pins it today, so the placement must say the
// mapping is assumed — and stop saying so once a layout has written the pin.
func TestDeclaredReportsWhetherTheCardOrderIsPinned(t *testing.T) {
	if Declared(labCore(), nil, nil, labInventory()).OrderPinned {
		t.Fatal("the profile does not set CUDA_DEVICE_ORDER")
	}
	if !Declared(labCore(), map[string]string{"CUDA_DEVICE_ORDER": "PCI_BUS_ID"}, nil, labInventory()).OrderPinned {
		t.Fatal("PCI_BUS_ID pins the order")
	}
}

// llama.cpp and the single-card vLLM Core declare no placement keys today:
// shown, never edited, and no invented GPU group.
func TestDeclaredWithoutPlacementKeys(t *testing.T) {
	p := Declared(&cores.Core{ID: "llamacpp", Engine: "llama.cpp", Ctx: 32768}, nil, nil, labInventory())
	if p.Editable || p.GPUs != nil || p.Embed.Device != "cpu" || p.MaxLen != 32768 {
		t.Fatalf("got %+v", p)
	}
}

func TestParseDevices(t *testing.T) {
	inv := labInventory()
	if got := parseDevices(" 0, 2 ,9", inv); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("unknown index must drop: %v", got)
	}
	if got := parseDevices("GPU-eeeeeeee", inv); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("uuid prefix: %v", got)
	}
	if got := parseDevices("0,1", Inventory{}); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("no inventory to check against: %v", got)
	}
}

// stub replaces nvidia-smi and /proc for one test. Outputs are matched on the
// first argument's prefix.
func stub(smiOut map[string]string, cgroups map[int]string) func() {
	oldSmi, oldCg := smi, cgroupOf
	smi = func(args ...string) (string, error) {
		for k, v := range smiOut {
			if strings.HasPrefix(args[0], k) {
				return v, nil
			}
		}
		return "", errors.New("not stubbed")
	}
	cgroupOf = func(pid int) string { return cgroups[pid] }
	return func() { smi, cgroupOf = oldSmi, oldCg }
}
