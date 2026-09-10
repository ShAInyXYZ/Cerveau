// ignite — the phone's doorbell and front door.
//
// Listens on 127.0.0.1:7701, fronted by tailscale serve (HTTPS on the
// ts.net domain). Requests other than status polling ensure the configured
// Cerveau stack is running
// via systemd user units (a no-op when already up — so simply opening the
// PWA wakes the machine), then reverse-proxies to the core on :7700.
// While the core is booting it serves a minimal auto-reloading "waking"
// page. The model server loads asynchronously (~1 min for 35B); the panel
// shows its status arriving.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const coreURL = "http://127.0.0.1:7700"

var wake = &wakeManager{load: configuredTarget, command: systemCommand, now: time.Now}

func systemCommand(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	return string(out), err
}

func coreUp(ctx context.Context) bool {
	client := http.Client{Timeout: 2 * time.Second}
	// Idle status reads service metadata; health probes can socket-wake a Core.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, coreURL+"/api/idle", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
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

func forwardedProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	// The core sees THIS proxy's connection, which is loopback, and would
	// otherwise grant remote callers the local trust reserved for someone
	// physically at the machine. Stamp every forwarded request so the gate
	// can tell them apart.
	// Rewrite runs after hop-by-hop removal. A client-supplied
	// Connection: X-Cerveau-Forwarded must not remove our authentication marker.
	proxy.Director = nil
	proxy.Rewrite = func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Host = r.In.Host
		r.SetXForwarded()
		r.Out.Header.Set(forwardedMarker, "1")
	}
	return proxy
}

func statusRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	// Session lists, state streams and system stats poll as well as the
	// explicit idle/health routes. Merely reading API state must not unpark.
	return strings.HasPrefix(r.URL.Path, "/api/")
}

func igniteHandler(manager *wakeManager, proxy http.Handler, ready func(context.Context) bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !statusRequest(r) {
			if err := manager.ensure(r.Context()); err != nil {
				log.Printf("wake: %v", err)
				w.Header().Set("Retry-After", "5")
				wakingPage(w)
				return
			}
		}
		if !ready(r.Context()) {
			wakingPage(w)
			return
		}
		// A client must never be able to strip or forge the marker: delete
		// any copy it supplied before the Director sets the real one.
		r.Header.Del(forwardedMarker)
		proxy.ServeHTTP(w, r)
	})
	mux.HandleFunc("/ignite/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		target, err := manager.load()
		if err != nil {
			http.Error(w, "Selected Core configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		for _, unit := range target.units() {
			st, err := manager.command(r.Context(), "is-active", unit)
			if err != nil && st == "" {
				st = "unavailable\n"
			}
			fmt.Fprintf(w, "%s: %s", unit, st)
		}
		fmt.Fprintf(w, "selected_core: %s\n", target.id)
		fmt.Fprintf(w, "core: %v\n", ready(r.Context()))
	})
	return mux
}

func main() {
	u, _ := url.Parse(coreURL)
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
	server := &http.Server{Addr: addr, Handler: igniteHandler(wake, forwardedProxy(u), coreUp), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
