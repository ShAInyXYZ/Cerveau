package api

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SystemStats reports live local hardware: GPU (nvidia-smi), CPU (/proc + k10temp),
// RAM (/proc/meminfo + a cached dmidecode brand read). This runs because crv is on
// the user's own machine — a browser can't read sensors.
func (a *API) SystemStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"gpu": gpuStats(),
		"cpu": cpuStats(),
		"ram": ramStats(),
	})
}

// ---- GPU ----
func gpuStats() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=name,temperature.gpu,utilization.gpu,memory.used,memory.total,power.draw,power.limit,fan.speed",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil
	}
	f := splitCSV(line)
	get := func(i int) float64 { if i < len(f) { v, _ := strconv.ParseFloat(strings.TrimSpace(f[i]), 64); return v }; return 0 }
	name := ""
	if len(f) > 0 { name = strings.TrimSpace(f[0]) }
	return map[string]any{
		"name":       name,
		"temp":       get(1),
		"util":       get(2),
		"mem_used":   get(3), // MiB
		"mem_total":  get(4),
		"power":      get(5),
		"power_max":  get(6),
		"fan":        get(7),
	}
}

// ---- CPU ----
var cpuBrand string
func cpuStats() map[string]any {
	if cpuBrand == "" {
		cpuBrand = readCPUBrand()
	}
	return map[string]any{
		"name":  cpuBrand,
		"cores": countCores(),
		"temp":  cpuTemp(),
		"util":  cpuUtil(),
	}
}

func readCPUBrand() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "CPU"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "model name") {
			if i := strings.Index(sc.Text(), ":"); i >= 0 {
				return strings.TrimSpace(sc.Text()[i+1:])
			}
		}
	}
	return "CPU"
}

func countCores() int {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "processor") {
			n++
		}
	}
	return n
}

// cpuTemp reads Tctl/Tccd from an AMD k10temp (or any coretemp) hwmon.
func cpuTemp() float64 {
	dirs, _ := os.ReadDir("/sys/class/hwmon")
	for _, d := range dirs {
		base := "/sys/class/hwmon/" + d.Name()
		name, _ := os.ReadFile(base + "/name")
		n := strings.TrimSpace(string(name))
		if n == "k10temp" || n == "coretemp" || n == "zenpower" {
			// prefer Tctl / Package label, else temp1
			if v := readMilliC(base + "/temp1_input"); v > 0 {
				return v
			}
		}
	}
	return 0
}

func readMilliC(path string) float64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0
	}
	return v / 1000.0
}

// cpuUtil samples /proc/stat twice ~120ms apart for an instantaneous %.
func cpuUtil() float64 {
	i1, t1 := readProcStat()
	time.Sleep(120 * time.Millisecond)
	i2, t2 := readProcStat()
	dt := t2 - t1
	if dt <= 0 {
		return 0
	}
	busy := (t2 - i2) - (t1 - i1)
	return busy / dt * 100
}

func readProcStat() (idle, total float64) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	line := strings.SplitN(string(b), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	var vals []float64
	for _, f := range fields[1:] {
		v, _ := strconv.ParseFloat(f, 64)
		vals = append(vals, v)
	}
	for _, v := range vals {
		total += v
	}
	if len(vals) >= 5 {
		idle = vals[3] + vals[4] // idle + iowait
	}
	return idle, total
}

// ---- RAM ----
var ramBrand map[string]any
func ramStats() map[string]any {
	used, total := memUsed()
	m := map[string]any{"used": used, "total": total} // MiB
	if ramBrand == nil {
		ramBrand = readRAMBrand()
	}
	for k, v := range ramBrand {
		m[k] = v
	}
	return m
}

func memUsed() (used, total float64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var memTotal, memAvail float64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseFloat(fields[1], 64) // kB
		switch fields[0] {
		case "MemTotal:":
			memTotal = v
		case "MemAvailable:":
			memAvail = v
		}
	}
	return (memTotal - memAvail) / 1024, memTotal / 1024
}

// readRAMBrand caches DDR type/speed/vendor via dmidecode (runs once; may be empty
// without permission — degrades to just used/total).
func readRAMBrand() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "dmidecode", "-t", "memory").Output()
	if err != nil {
		return map[string]any{}
	}
	m := map[string]any{}
	sticks := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Type:") && !strings.Contains(line, "Error") && m["type"] == nil && strings.Contains(line, "DDR"):
			m["type"] = strings.TrimSpace(strings.TrimPrefix(line, "Type:"))
		case strings.HasPrefix(line, "Configured Memory Speed:") && m["speed"] == nil:
			m["speed"] = strings.TrimSpace(strings.TrimPrefix(line, "Configured Memory Speed:"))
		case strings.HasPrefix(line, "Manufacturer:") && m["vendor"] == nil && !strings.Contains(line, "Unknown"):
			m["vendor"] = strings.TrimSpace(strings.TrimPrefix(line, "Manufacturer:"))
		case strings.HasPrefix(line, "Size:") && strings.Contains(line, "GB"):
			sticks++
		}
	}
	if sticks > 0 {
		m["sticks"] = sticks
	}
	return m
}

func splitCSV(s string) []string { return strings.Split(s, ",") }
