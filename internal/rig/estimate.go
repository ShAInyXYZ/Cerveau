package rig

import (
	"fmt"
	"strings"
)

// Does this model fit this placement — and with how much context?
//
// An ESTIMATE, from arithmetic: used memory cannot answer it, because vLLM
// fills every card it is given to its share by design, so a healthy Core
// reads "full" whatever the model's size. Per card:
//
//	share of the card  =  weights ÷ cards  +  overhead  +  room for the KV cache
//
// and the window fits when a full-length sequence's KV fits in that room.

// overheadMiB is what a rank needs beyond weights and KV: activations, CUDA
// graphs, the collective buffers. Measured on the lab rig at 2–3 GiB.
const overheadMiB = 2560

type CardFit struct {
	GPU      int `json:"gpu"`
	Total    int `json:"total"` // MiB, all of them
	Busy     int `json:"busy"`  // held by things that are not a Core or the embedder
	Budget   int `json:"budget"`
	Weights  int `json:"weights"`
	Overhead int `json:"overhead"`
	KVRoom   int `json:"kv_room"`
	KVNeeded int `json:"kv_needed"`
}

type Fit struct {
	Verdict string `json:"verdict"` // fits · tight · no · unknown
	// Reasons say why, in the order that matters. For "fits" it is the margin.
	Reasons    []string  `json:"reasons"`
	Cards      []CardFit `json:"cards"`
	Window     int       `json:"window"`
	MaxWindow  int       `json:"max_window"`
	GroupSizes []int     `json:"group_sizes"`
	Remote     bool      `json:"remote"`
}

func Estimate(f *ModelFacts, inv Inventory, busy map[int]int, gpus []int, share float64, kv string, window int) Fit {
	fit := Fit{Verdict: "unknown", Reasons: []string{}, Cards: []CardFit{}, Window: window, GroupSizes: f.GroupSizes(len(inv.GPUs))}
	if f == nil || f.WeightBytes == 0 {
		fit.Reasons = append(fit.Reasons, "the checkpoint's size is not known, so nothing can be estimated")
		return fit
	}
	fit.Remote = f.Remote
	n := len(gpus)
	if n == 0 || share <= 0 {
		return fit
	}
	known := map[int]GPU{}
	for _, g := range inv.GPUs {
		known[g.Index] = g
	}
	weights := int(f.WeightBytes / int64(n) >> 20)
	perToken := f.kvBytesPerToken(n, kv) // per card
	room := -1
	for _, i := range gpus {
		g := known[i]
		c := CardFit{GPU: i, Total: g.MemTotal, Busy: busy[i], Budget: int(float64(g.MemTotal) * share), Weights: weights, Overhead: overheadMiB}
		c.KVRoom = c.Budget - c.Weights - c.Overhead
		if c.KVRoom < 0 {
			c.KVRoom = 0
		}
		if perToken > 0 {
			c.KVNeeded = int(perToken * int64(window) >> 20)
		}
		if room < 0 || c.KVRoom < room {
			room = c.KVRoom // every rank holds the same pool: the smallest card sets it
		}
		fit.Cards = append(fit.Cards, c)
	}
	if perToken > 0 {
		fit.MaxWindow = int(int64(room) << 20 / perToken)
	}

	var no, tight []string
	for _, c := range fit.Cards {
		switch {
		case c.Weights+c.Overhead > c.Budget:
			no = append(no, fmt.Sprintf("GPU %d: the weights need %s and the runtime about %s, but %.0f%% of the card is %s", c.GPU, gib(c.Weights), gib(c.Overhead), share*100, gib(c.Budget)))
		case c.Total-c.Busy < c.Budget:
			no = append(no, fmt.Sprintf("GPU %d: other things hold %s, which leaves less than the %s the Core asks for — it would refuse to start", c.GPU, gib(c.Busy), gib(c.Budget)))
		}
	}
	if len(no) == 0 && perToken > 0 && window > 0 {
		need := fit.Cards[0].KVNeeded
		switch {
		case need > room:
			no = append(no, fmt.Sprintf("a full %s window needs %s of KV cache per card and %s is left — the largest window that fits is about %s", tokens(window), gib(need), gib(room), tokens(fit.MaxWindow)))
		case float64(room) < 1.15*float64(need):
			tight = append(tight, fmt.Sprintf("a full %s window takes %s of the %s left per card — it fits, with almost nothing to spare", tokens(window), gib(need), gib(room)))
		}
	}
	switch {
	case len(no) > 0:
		fit.Verdict, fit.Reasons = "no", no
	case len(tight) > 0:
		fit.Verdict, fit.Reasons = "tight", tight
	case perToken == 0:
		fit.Verdict = "fits"
		fit.Reasons = append(fit.Reasons, fmt.Sprintf("the weights fit with %s to spare per card; this checkpoint does not say how much a token of context costs", gib(room)))
	default:
		fit.Verdict = "fits"
		fit.Reasons = append(fit.Reasons, fmt.Sprintf("%s of weights per card, %s left for the KV cache — room for about %s of context, %s asked", gib(weights), gib(room), tokens(fit.MaxWindow), tokens(window)))
	}
	if f.Remote {
		fit.Reasons = append(fit.Reasons, "the weights are on a network mount: every cold start reads "+gib(int(f.WeightBytes>>20))+" over it")
	}
	return fit
}

// kvBytesPerToken is what one token of context costs ONE card: keys and
// values, on the layers that keep them, for this card's share of the KV heads.
func (f *ModelFacts) kvBytesPerToken(cards int, kv string) int64 {
	if f.AttnLayers == 0 || f.KVHeads == 0 || f.HeadDim == 0 {
		return 0
	}
	heads := f.KVHeads / cards
	if heads < 1 {
		heads = 1 // fewer KV heads than cards: each card keeps a whole copy
	}
	width := int64(2) // bf16 / fp16 / auto
	if strings.HasPrefix(strings.ToLower(kv), "fp8") {
		width = 1
	}
	return 2 * int64(f.AttnLayers) * int64(heads) * int64(f.HeadDim) * width
}

func gib(mib int) string { return fmt.Sprintf("%.1f GB", float64(mib)/1024) }

func tokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM tokens", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%dK tokens", n/1024)
	}
	return fmt.Sprintf("%d tokens", n)
}
