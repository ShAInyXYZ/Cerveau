// ignite — the phone's doorbell and front door.
//
// Listens on 127.0.0.1:7701, fronted by tailscale serve (HTTPS on the
// ts.net domain). Every request first ensures the Cerveau stack is running
// via systemd user units (a no-op when already up — so simply opening the
// PWA wakes the machine), then reverse-proxies to the core on :7700.
// While the core is booting it serves a minimal auto-reloading "waking"
// page. The model server loads asynchronously (~1 min for 35B); the panel
// shows its status arriving.
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"time"
)

const coreURL = "http://127.0.0.1:7700"

var (
	proxy   *httputil.ReverseProxy
	mu      sync.Mutex
	lastTry time.Time
)

func ensureStarted() {
	mu.Lock()
	defer mu.Unlock()
	if time.Since(lastTry) < 5*time.Second { // throttle unit starts
		return
	}
	lastTry = time.Now()
	for _, unit := range []string{"cerveau.service", "cerveau-llama.service", "cerveau-embed.service"} {
		if out, err := exec.Command("systemctl", "--user", "start", unit).CombinedOutput(); err != nil {
			log.Printf("start %s: %v: %s", unit, err, out)
		}
	}
}

func coreUp() bool {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(coreURL + "/api/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func wakingPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="theme-color" content="#09090B"><title>Cerveau — waking</title>
<style>body{background:#09090B;color:#FAFAFA;font:14px ui-monospace,monospace;
display:flex;height:100vh;margin:0;align-items:center;justify-content:center}
.b{color:#E54866}.blink{animation:b 1.1s steps(2) infinite}@keyframes b{50%{opacity:.3}}</style>
</head><body><div><span class="b">◈</span> waking cerveau <span class="blink">…</span></div>
<script>setTimeout(()=>location.reload(),2000)</script></body></html>`)
}

// must match internal/server.ForwardedMarker
const forwardedMarker = "X-Cerveau-Forwarded"

func main() {
	u, _ := url.Parse(coreURL)
	proxy = httputil.NewSingleHostReverseProxy(u)
	// The core sees THIS proxy's connection, which is loopback, and would
	// otherwise grant remote callers the local trust reserved for someone
	// physically at the machine. Stamp every forwarded request so the gate
	// can tell them apart.
	inner := proxy.Director
	proxy.Director = func(r *http.Request) {
		inner(r)
		r.Header.Set(forwardedMarker, "1")
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ensureStarted()
		if !coreUp() {
			wakingPage(w)
			return
		}
		// A client must never be able to strip or forge the marker: delete
		// any copy it supplied before the Director sets the real one.
		r.Header.Del(forwardedMarker)
		proxy.ServeHTTP(w, r)
	})
	http.HandleFunc("/ignite/state", func(w http.ResponseWriter, r *http.Request) {
		for _, unit := range []string{"cerveau.service", "cerveau-llama.service", "cerveau-embed.service"} {
			st, _ := exec.Command("systemctl", "--user", "is-active", unit).Output()
			fmt.Fprintf(w, "%s: %s", unit, st)
		}
		fmt.Fprintf(w, "core: %v\n", coreUp())
	})
	addr := os.Getenv("CRV_IGNITE_ADDR")
	if addr == "" {
		// Bind every interface, not one hardcoded tailnet IP: Tailscale can
		// reassign addresses, and the NAS gate proxies IN from the tailnet —
		// binding a single literal made the doorbell unreachable from it.
		// Exposure is still tailnet-only: nothing forwards this port publicly,
		// and every /api route is gated by token + device signature.
		addr = ":7701"
	}
	log.Printf("ignite listening on http://%s (wakes the stack, proxies to %s)", addr, coreURL)
	log.Fatal(http.ListenAndServe(addr, nil))
}
