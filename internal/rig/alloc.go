package rig

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"cerveau/internal/cores"
)

// EmbedUnit is the embedder's systemd unit (deploy/idle/cerveau-embed.service).
const EmbedUnit = "cerveau-embed.service"

// Two different questions, kept apart on purpose:
//
//   - OBSERVED is what is on the cards right now, as the driver reports it.
//   - DECLARED is what the active Core will run with on its NEXT start: the
//     profile's defaults with the user's overrides applied.
//
// They disagree whenever the Core is parked, or a parameter was saved and not
// yet applied. The panel shows the difference; blending them would hide it.

// Consumer is one process holding memory on one card.
type Consumer struct {
	PID  int    `json:"pid"`
	Mem  int    `json:"mem"` // MiB
	Name string `json:"name"`
	// Unit is the systemd unit or scope whose cgroup the process lives in.
	// vLLM's tensor-parallel workers are children of the Core unit, so they
	// land here without any guessing from process names.
	Unit string `json:"unit,omitempty"`
	Role string `json:"role"`           // core | embedder | other
	Core string `json:"core,omitempty"` // the Core's id when Role is core
}

// CardUse is one card's memory right now. Used is everything the driver
// counts; the consumers are only the COMPUTE processes, so on a card that also
// drives a display the two do not add up — the rest is the desktop.
type CardUse struct {
	GPU       int        `json:"gpu"`
	Used      int        `json:"used"` // MiB
	Consumers []Consumer `json:"consumers"`
}

// cgroupOf reads /proc/<pid>/cgroup. A variable so tests need no live pid.
var cgroupOf = func(pid int) string {
	data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	return string(data)
}

// Observed attributes the memory on every card to the Core, the embedder or
// something else, using the registry's unit names.
func Observed(inv Inventory, reg *cores.Registry) []CardUse {
	if len(inv.GPUs) == 0 {
		return nil
	}
	byUUID := map[string]int{}
	use := make([]CardUse, len(inv.GPUs))
	for i, g := range inv.GPUs {
		byUUID[g.UUID] = i
		use[i] = CardUse{GPU: g.Index, Consumers: []Consumer{}}
	}
	if out, err := smi("--query-gpu=uuid,memory.used", "--format=csv,noheader,nounits"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if uuid, used, ok := strings.Cut(line, ","); ok {
				if i, ok := byUUID[strings.TrimSpace(uuid)]; ok {
					use[i].Used = atoi(used)
				}
			}
		}
	}
	out, err := smi("--query-compute-apps=gpu_uuid,pid,used_memory,process_name", "--format=csv,noheader,nounits")
	if err != nil {
		return use
	}
	for _, a := range parseApps(out) {
		i, ok := byUUID[a.uuid]
		if !ok {
			continue
		}
		c := Consumer{PID: a.pid, Mem: a.mem, Name: a.name, Unit: unitOf(cgroupOf(a.pid))}
		c.Role, c.Core = roleOf(c.Unit, reg)
		use[i].Consumers = append(use[i].Consumers, c)
	}
	return use
}

type app struct {
	uuid string
	pid  int
	mem  int
	name string
}

func parseApps(out string) []app {
	var apps []app
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(line, ",", 4)
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(f[1]))
		if err != nil {
			continue
		}
		a := app{uuid: strings.TrimSpace(f[0]), pid: pid, mem: atoi(f[2])}
		if len(f) == 4 {
			a.name = strings.TrimSpace(f[3])
		}
		apps = append(apps, a)
	}
	return apps
}

// unitOf returns the innermost systemd unit or scope in a cgroup path:
// ".../user@1000.service/app.slice/crv-core-x.service" is crv-core-x.service,
// not the user manager above it.
func unitOf(cgroup string) string {
	for _, line := range strings.Split(strings.TrimSpace(cgroup), "\n") {
		_, path, ok := strings.Cut(line, "::") // cgroup v2: "0::/path"
		if !ok {
			continue
		}
		seg := strings.Split(path, "/")
		for i := len(seg) - 1; i >= 0; i-- {
			if strings.HasSuffix(seg[i], ".service") || strings.HasSuffix(seg[i], ".scope") {
				return seg[i]
			}
		}
	}
	return ""
}

func roleOf(unit string, reg *cores.Registry) (role, core string) {
	if unit == "" {
		return "other", ""
	}
	if unit == EmbedUnit {
		return "embedder", ""
	}
	if reg != nil {
		for _, c := range reg.Cores {
			if c.Unit == unit {
				return "core", c.ID
			}
		}
	}
	return "other", ""
}

// Placement is where a Core sits on its next start.
type Placement struct {
	Core   string `json:"core"`
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Model  string `json:"model,omitempty"`
	// GPUs is the Core's group, as card indices. Nil when the profile does not
	// say — the engine then sees every card, and the canvas must not pretend
	// to know which ones it will take.
	GPUs    []int          `json:"gpus"`
	TP      int            `json:"tp,omitempty"`
	GPUUtil float64        `json:"gpu_util,omitempty"`
	KV      string         `json:"kv,omitempty"`
	MaxLen  int            `json:"max_len,omitempty"`
	Embed   EmbedPlacement `json:"embed"`
	// Editable: the profile declares the placement keys, so a drawn layout has
	// somewhere to be written. A Core without them is shown, never edited.
	Editable bool `json:"editable"`
	// Overridden: the user has moved this Core off its profile's placement.
	Overridden bool `json:"overridden"`
	// OrderPinned: CUDA_DEVICE_ORDER=PCI_BUS_ID is in effect, so an index in
	// GPUs is the card nvidia-smi shows under that index. Without it CUDA
	// numbers cards fastest-first, and on a machine with mixed cards the two
	// orders can disagree — the canvas then says the mapping is assumed.
	OrderPinned bool `json:"order_pinned"`
	// Params is everything the profile runs with — defaults with the user's
	// overrides applied — so the panel can draw a profile's configuration
	// without a second request.
	Params map[string]string `json:"params"`
	// Live: this is the Core the harness is talking to. Any other profile is a
	// PREVIEW: where it would go, not where anything is.
	Live bool `json:"live"`
}

// EmbedPlacement is where the embedder runs under a Core. No `embed` block in
// the profile means CPU — what a one-GPU machine wants.
type EmbedPlacement struct {
	Device string `json:"device"` // cuda | cpu
	GPUs   []int  `json:"gpus,omitempty"`
}

// The keys a layout is written in. Both TP=4 profiles declare them.
const (
	keyDevices = "CUDA_VISIBLE_DEVICES"
	keyTP      = "TP"
	keyOrder   = "CUDA_DEVICE_ORDER"
)

// Placeable: the profile declares the keys a placement is written in, so a
// drawn layout has somewhere to go. The one definition — Declared, PlanLayout
// and the profile list all ask here.
func Placeable(c *cores.Core) bool {
	_, dev := c.Params[keyDevices]
	_, tp := c.Params[keyTP]
	return dev && tp
}

// Profile is one line of the profile list: enough to offer it, not to draw it.
type Profile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Engine   string `json:"engine"`
	Model    string `json:"model,omitempty"`
	Live     bool   `json:"live"`
	Editable bool   `json:"editable"`
}

// Profiles lists every Core in the registry; liveID is the one answering.
func Profiles(reg *cores.Registry, liveID string) []Profile {
	out := make([]Profile, 0, len(reg.Cores))
	for i := range reg.Cores {
		c := &reg.Cores[i]
		out = append(out, Profile{ID: c.ID, Name: c.Name, Engine: c.Engine, Model: c.Model, Live: liveID != "" && c.ID == liveID, Editable: Placeable(c)})
	}
	return out
}

// BusyMemory is what each card holds that a Core switch does NOT free: not a
// Core (the watchdog stops those) and not the embedder (the plan places it).
// It is what an estimate must leave room for.
func BusyMemory(use []CardUse) map[int]int {
	busy := map[int]int{}
	for _, u := range use {
		left := u.Used
		for _, c := range u.Consumers {
			if c.Role == "core" || c.Role == "embedder" {
				left -= c.Mem
			}
		}
		if left > 0 {
			busy[u.GPU] = left
		}
	}
	return busy
}

// EmbedderMemory is what the embedder holds right now, MiB — zero when it is
// not on a card. A plan uses it instead of a guess when it can.
func EmbedderMemory(use []CardUse) int {
	total := 0
	for _, u := range use {
		for _, c := range u.Consumers {
			if c.Role == "embedder" {
				total += c.Mem
			}
		}
	}
	return total
}

// Declared resolves a Core's next-start placement from its profile defaults
// and the overrides on disk. Pure: the caller loads the two override sets.
func Declared(c *cores.Core, ov, embedOv map[string]string, inv Inventory) Placement {
	eff := cores.Effective(c.Params, ov)
	p := Placement{
		Core: c.ID, Name: c.Name, Engine: c.Engine, Model: c.Model, Params: eff,
		GPUs:   parseDevices(eff[keyDevices], inv),
		TP:     atoi(eff[keyTP]),
		KV:     eff["KV"],
		MaxLen: atoi(eff["MAX_LEN"]),
	}
	p.GPUUtil, _ = strconv.ParseFloat(eff["GPU_UTIL"], 64)
	if p.MaxLen == 0 {
		p.MaxLen = c.Ctx
	}
	p.Editable = Placeable(c)
	_, movedDev := ov[keyDevices]
	_, movedTP := ov[keyTP]
	_, movedEmbed := embedOv[keyDevices]
	_, movedEmbedDev := embedOv["EMBED_DEVICE"]
	p.Overridden = movedDev || movedTP || movedEmbed || movedEmbedDev
	p.OrderPinned = eff[keyOrder] == "PCI_BUS_ID"

	e := cores.Effective(c.Embed, embedOv)
	p.Embed = EmbedPlacement{Device: "cpu"}
	if strings.EqualFold(e["EMBED_DEVICE"], "cuda") {
		p.Embed = EmbedPlacement{Device: "cuda", GPUs: parseDevices(e[keyDevices], inv)}
	}
	return p
}

// parseDevices reads a CUDA_VISIBLE_DEVICES list: card indices, or the UUIDs
// CUDA also accepts. Entries that name no known card are dropped.
func parseDevices(s string, inv Inventory) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	known := map[int]bool{}
	for _, g := range inv.GPUs {
		known[g.Index] = true
	}
	out := []int{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if n, err := strconv.Atoi(part); err == nil {
			// with no inventory (nvidia-smi absent) there is nothing to check against
			if len(inv.GPUs) == 0 || known[n] {
				out = append(out, n)
			}
			continue
		}
		for _, g := range inv.GPUs {
			if part != "" && strings.HasPrefix(g.UUID, part) {
				out = append(out, g.Index)
				break
			}
		}
	}
	return out
}
