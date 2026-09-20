// Package rig describes the machine a Brain Core runs on: which GPUs it has,
// how they are linked, what is sitting on them right now, and where the active
// profile will put the Core on its next start.
//
// It is description and arithmetic only. Nothing here changes a Core: a layout
// the user draws becomes a params diff that travels through internal/cores
// like any other parameter change, and the park watchdog applies it.
package rig

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GPU is what a card IS — the facts that do not move while the machine is up.
// Temperature, load and memory in use are live and stay in /api/system/stats.
type GPU struct {
	Index      int     `json:"index"`
	UUID       string  `json:"uuid"`
	Name       string  `json:"name"`
	MemTotal   int     `json:"mem_total"` // MiB
	PCIeGen    int     `json:"pcie_gen,omitempty"`
	PCIeWidth  int     `json:"pcie_width,omitempty"`
	PowerLimit float64 `json:"power_limit,omitempty"` // W
	ComputeCap string  `json:"compute_cap,omitempty"`
}

// Inventory is every GPU nvidia-smi sees, in index order, and how each pair is
// connected. A machine without nvidia-smi has an empty inventory — that is a
// CPU-only box, not an error.
type Inventory struct {
	GPUs []GPU `json:"gpus"`
	// Links[i][j] is nvidia-smi's word for the path between card i and card j:
	// "X" (self), "NV#" (NVLink), "PIX"/"PXB"/"PHB"/"NODE"/"SYS" (PCIe, nearest
	// to farthest). Empty when the topology could not be read.
	Links  [][]string `json:"links,omitempty"`
	NVLink bool       `json:"nvlink"`
}

// smi runs nvidia-smi with a short deadline. A variable so tests never need a GPU.
var smi = func(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", args...).Output()
	return string(out), err
}

var (
	invMu   sync.Mutex
	invAt   time.Time
	invLast Inventory
)

// Probe reads the inventory, at most once a minute: the cards do not change
// while the panel is open, and two nvidia-smi calls per canvas refresh would
// cost more than everything else on the page.
func Probe() Inventory {
	invMu.Lock()
	defer invMu.Unlock()
	if !invAt.IsZero() && time.Since(invAt) < time.Minute {
		return invLast
	}
	var inv Inventory
	if out, err := smi("--query-gpu=index,uuid,name,memory.total,pcie.link.gen.max,pcie.link.width.max,power.limit,compute_cap",
		"--format=csv,noheader,nounits"); err == nil {
		inv.GPUs = parseGPUs(out)
	}
	if len(inv.GPUs) > 1 {
		if out, err := smi("topo", "-m"); err == nil {
			inv.Links = parseTopo(out, len(inv.GPUs))
			inv.NVLink = hasNVLink(inv.Links)
		}
	}
	invLast, invAt = inv, time.Now()
	return inv
}

func parseGPUs(out string) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 4 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		g := GPU{Index: idx, UUID: f[1], Name: f[2], MemTotal: atoi(f[3])}
		// "[N/A]" on cards that do not report a field parses to zero, and zero
		// is omitted from the JSON: absent, not wrong.
		if len(f) > 5 {
			g.PCIeGen, g.PCIeWidth = atoi(f[4]), atoi(f[5])
		}
		if len(f) > 6 {
			g.PowerLimit, _ = strconv.ParseFloat(f[6], 64)
		}
		if len(f) > 7 && !strings.Contains(f[7], "N/A") {
			g.ComputeCap = f[7]
		}
		gpus = append(gpus, g)
	}
	return gpus
}

var (
	ansiRe   = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	gpuRowRe = regexp.MustCompile(`^GPU(\d+)$`)
)

// parseTopo reads the GPU×GPU block of `nvidia-smi topo -m`. GPU columns come
// first in every row, so the n cells after the label are the ones wanted; the
// NIC and affinity columns that follow are ignored.
func parseTopo(out string, n int) [][]string {
	links := make([][]string, n)
	seen := 0
	for _, line := range strings.Split(ansiRe.ReplaceAllString(out, ""), "\n") {
		f := strings.Split(line, "\t")
		m := gpuRowRe.FindStringSubmatch(strings.TrimSpace(f[0]))
		if m == nil || len(f) < n+1 {
			continue
		}
		i, _ := strconv.Atoi(m[1])
		if i >= n {
			continue
		}
		row := make([]string, n)
		for j := 0; j < n; j++ {
			row[j] = strings.TrimSpace(f[j+1])
		}
		links[i] = row
		seen++
	}
	if seen != n {
		return nil
	}
	return links
}

func hasNVLink(links [][]string) bool {
	for _, row := range links {
		for _, c := range row {
			if strings.HasPrefix(c, "NV") {
				return true
			}
		}
	}
	return false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
