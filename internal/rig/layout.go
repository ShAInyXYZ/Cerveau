package rig

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"cerveau/internal/cores"
)

// A layout is what the user drew: which cards the Core takes, and where the
// embedder goes. PlanLayout turns it into the profile's own parameters.
//
// It writes nothing. The answer carries the COMPLETE override sets to save —
// what the user already changed, with the placement keys set — because
// PUT /api/cores/{id}/params replaces the file: sending the placement keys
// alone would silently delete a saved GPU_UTIL or KV.

type LayoutRequest struct {
	Core  string         `json:"core"`
	GPUs  []int          `json:"gpus"`
	Embed EmbedPlacement `json:"embed"`
	// GPUUtil is the share of each card the Core claims (vLLM's
	// gpu-memory-utilization). Zero leaves what the profile has.
	GPUUtil float64 `json:"gpu_util,omitempty"`
}

type Plan struct {
	// Core is the profile this plan was made for. A plan is only ever saved
	// to that profile: the panel checks it, because the profile on screen can
	// change under an open page when another device switches the live Core.
	Core           string            `json:"core"`
	Overrides      map[string]string `json:"overrides"`
	EmbedOverrides map[string]string `json:"embed_overrides"`
	// Changed is what saving would change, in the user's terms:
	// "Core CUDA_VISIBLE_DEVICES 0,1,2,3 → 2,3".
	Changed []string `json:"changed"`
	// Problems block a save. Warnings do not.
	Problems []string `json:"problems"`
	Warnings []string `json:"warnings"`
	// Fit is the estimate for this placement; the handler fills it in, because
	// it needs the checkpoint on disk and what else is on the cards.
	Fit *Fit `json:"fit,omitempty"`
	// What the estimate was made with, so the caller need not re-derive them.
	GPUs    []int   `json:"gpus"`
	GPUUtil float64 `json:"gpu_util"`
	KV      string  `json:"kv"`
	Window  int     `json:"window"`
}

// defaultEmbedMiB is the room assumed for an embedder nobody has measured yet.
// Nemotron-3-Embed-1B holds about 2.3 GiB on the lab rig's 3060; when the
// embedder is running, PlanInput.EmbedMiB carries what it really holds.
const defaultEmbedMiB = 3072

// PlanInput is everything a plan is made from besides what the user drew.
type PlanInput struct {
	Core           *cores.Core
	Overrides      map[string]string // what the user has already changed, from cores.d
	EmbedOverrides map[string]string
	Inventory      Inventory
	// Facts may be nil: the group-size rule then falls back to powers of two.
	Facts *ModelFacts
	// EmbedMiB is the embedder's measured footprint; zero means not measured.
	EmbedMiB int
}

func PlanLayout(in PlanInput, req LayoutRequest) Plan {
	c, ov, embedOv, inv, facts := in.Core, in.Overrides, in.EmbedOverrides, in.Inventory, in.Facts
	p := Plan{Core: c.ID, Overrides: copyMap(ov), EmbedOverrides: copyMap(embedOv), Changed: []string{}, Problems: []string{}, Warnings: []string{}, GPUs: []int{}}
	// a quarter again over what was measured: the embedder's batches breathe
	embedRoom := defaultEmbedMiB
	if in.EmbedMiB > 0 {
		embedRoom = in.EmbedMiB + in.EmbedMiB/4
	}
	if !Placeable(c) {
		p.Problems = append(p.Problems, "this profile does not declare "+keyDevices+" and "+keyTP+", so there is nowhere to write a placement")
		return p
	}

	known := map[int]GPU{}
	for _, g := range inv.GPUs {
		known[g.Index] = g
	}
	gpus := uniqueSorted(req.GPUs)
	for _, i := range gpus {
		if _, ok := known[i]; !ok {
			p.Problems = append(p.Problems, fmt.Sprintf("GPU %d is not in this machine", i))
		}
	}
	switch n := len(gpus); {
	case n == 0:
		p.Problems = append(p.Problems, "the Core needs at least one card")
	case !facts.okGroup(n):
		// Tensor parallelism splits the attention heads evenly across the
		// cards: the legal sizes come from the model's own head counts, or,
		// when the checkpoint cannot be read, the powers of two.
		sizes := facts.GroupSizes(len(inv.GPUs))
		p.Problems = append(p.Problems, fmt.Sprintf("%d cards cannot share this model evenly — it runs on %s", n, orList(sizes)))
	}
	if req.GPUUtil != 0 && (req.GPUUtil < 0.2 || req.GPUUtil > 0.98) {
		p.Problems = append(p.Problems, "the share of each card must be between 20% and 98%")
	}
	if len(p.Problems) > 0 {
		return p
	}

	smallest, mixed := known[gpus[0]].MemTotal, false
	for _, i := range gpus[1:] {
		if m := known[i].MemTotal; m != smallest {
			mixed = true
			if m < smallest {
				smallest = m
			}
		}
	}
	if mixed {
		p.Warnings = append(p.Warnings, fmt.Sprintf("these cards differ in size: every one of them is used as if it had %d GB", smallest/1024))
	}

	eff := cores.Effective(c.Params, ov)
	// who names whose parameter it is: both files carry a CUDA_VISIBLE_DEVICES
	set := func(who string, target map[string]string, from map[string]string, k, v string) {
		if from[k] != v {
			p.Changed = append(p.Changed, fmt.Sprintf("%s %s %s → %s", who, k, orDash(from[k]), orDash(v)))
		}
		target[k] = v
	}
	set("Core", p.Overrides, eff, keyDevices, joinInts(gpus))
	set("Core", p.Overrides, eff, keyTP, strconv.Itoa(len(gpus)))
	// nvidia-smi counts cards in bus order, CUDA fastest-first. Pinned, an
	// index means the card the user clicked.
	set("Core", p.Overrides, eff, keyOrder, "PCI_BUS_ID")
	if req.GPUUtil != 0 {
		set("Core", p.Overrides, eff, "GPU_UTIL", strconv.FormatFloat(req.GPUUtil, 'f', -1, 64))
	}
	after := cores.Effective(c.Params, p.Overrides)
	p.GPUs, p.KV = gpus, after["KV"]
	p.GPUUtil, _ = strconv.ParseFloat(after["GPU_UTIL"], 64)
	if p.Window = atoi(after["MAX_LEN"]); p.Window == 0 {
		p.Window = c.Ctx
	}

	eeff := cores.Effective(c.Embed, embedOv)
	switch strings.ToLower(req.Embed.Device) {
	case "cuda":
		eg := uniqueSorted(req.Embed.GPUs)
		if len(eg) != 1 {
			p.Problems = append(p.Problems, "the embedder runs on exactly one card, or on the CPU")
			break
		}
		g, ok := known[eg[0]]
		if !ok {
			p.Problems = append(p.Problems, fmt.Sprintf("GPU %d is not in this machine", eg[0]))
			break
		}
		if contains(gpus, eg[0]) {
			util, _ := strconv.ParseFloat(p.Overrides["GPU_UTIL"], 64)
			if util == 0 {
				util, _ = strconv.ParseFloat(eff["GPU_UTIL"], 64)
			}
			if left := int(float64(g.MemTotal) * (1 - util)); util > 0 && left < embedRoom {
				p.Problems = append(p.Problems, fmt.Sprintf("GPU %d is filled to %.0f%% by the Core, which leaves %.1f GB — the embedder needs about %.1f", eg[0], util*100, float64(left)/1024, float64(embedRoom)/1024))
			} else {
				p.Warnings = append(p.Warnings, fmt.Sprintf("the embedder shares GPU %d with the Core", eg[0]))
			}
		}
		set("embedder", p.EmbedOverrides, eeff, "EMBED_DEVICE", "cuda")
		set("embedder", p.EmbedOverrides, eeff, keyDevices, joinInts(eg))
		set("embedder", p.EmbedOverrides, eeff, keyOrder, "PCI_BUS_ID")
	case "cpu", "":
		set("embedder", p.EmbedOverrides, eeff, "EMBED_DEVICE", "cpu")
	default:
		p.Problems = append(p.Problems, fmt.Sprintf("the embedder runs on cuda or cpu, not %q", req.Embed.Device))
	}
	sort.Strings(p.Changed)
	return p
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+3)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func uniqueSorted(in []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func joinInts(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, ",")
}

// orList: "1, 2, 4 or 8"
func orList(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = strconv.Itoa(x)
	}
	if len(s) < 2 {
		return strings.Join(s, "") + " card"
	}
	return strings.Join(s[:len(s)-1], ", ") + " or " + s[len(s)-1]
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
