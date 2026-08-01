package api

import (
	"net/http"
	"os"
	"strings"

	"cerveau/internal/rfx"
)

// requireJSON gates the mutating RFX endpoints. A cross-origin page — or the
// sandboxed panel iframe itself (opaque origin) — cannot send
// application/json without a CORS preflight, which the core never grants.
// So these endpoints are reachable only by same-origin panel code and local
// tools, and a custom panel's ONLY road to execution is the postMessage
// bridge the host mediates.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	return true
}

// panelBridge is injected before the pack's own HTML. It is the only door
// out of the sandboxed iframe: rfx.run / rfx.on / rfx.resize over
// postMessage. The host validates every request (pack membership, dangerous
// confirm) before anything reaches the guarded registry.
const panelBridge = `<style>
/* RFX_UI base — the Cerveau identity, free to every panel. Authors may
   override anything; this exists so a bare panel already looks native
   (tokens, thin themed scrollbars) instead of browser-default. */
:root {
  --bg:#17140F; --s1:#1E1A14; --s2:#27221A; --line:#363026; --line2:#4a4438;
  --text:#F2E1DE; --muted:#B8B2A6; --dim:#87816F;
  --accent:#E88BA0; --ok:#4bb894; --err:#e6533f; --warn:#cf9f2e;
  --mono:ui-monospace,'SF Mono',SFMono-Regular,Menlo,Consolas,monospace;
}
* { scrollbar-width: thin; scrollbar-color: var(--line2) transparent; }
*::-webkit-scrollbar { width: 6px; height: 6px; }
*::-webkit-scrollbar-thumb { background: var(--line2); border-radius: 3px; }
*::-webkit-scrollbar-track { background: transparent; }
html, body { margin: 0; background: transparent; color: var(--text); }
body { font: 12px/1.5 system-ui, sans-serif; }
</style>
<script>
(function () {
  const pending = new Map(); let seq = 0; const subs = []; let lastH = 0;
  window.rfx = {
    run(name, args) {
      return new Promise((resolve) => {
        const id = ++seq; pending.set(id, resolve);
        parent.postMessage({ rfx: "run", id, name, args: args || {} }, "*");
      });
    },
    on(cb) { subs.push(cb); },
    // resize is de-bounced against feedback loops: measuring the body while
    // the body fills the iframe inflates every readout — panels should pass
    // a CONTENT height, and identical-ish requests are dropped here.
    resize(h) {
      h = Math.round(h);
      if (Math.abs(h - lastH) < 4) return;
      lastH = h;
      parent.postMessage({ rfx: "resize", h }, "*");
    },
    // fit(): measure the first element in <body> (the content root) and
    // resize to it — the safe way to autosize.
    fit() {
      const el = document.body.firstElementChild;
      if (el) this.resize(el.scrollHeight + 24);
    }
  };
  addEventListener("message", (e) => {
    const m = e.data || {};
    if (m.rfx === "result" && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
    if (m.rfx === "event") subs.forEach((f) => f(m.event));
  });
})();
</script>
`

// PanelRfx serves a pack's custom ui/panel.html (RFX-UI tier 2) for the
// sandboxed dock iframe. The CSP closes every road except the bridge: no
// fetch, no frames, no forms, no external anything. Full presentation
// freedom; zero capability beyond the pack's own reflexes.
func (a *API) PanelRfx(w http.ResponseWriter, r *http.Request) {
	if a.rfxLoader == nil {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("pack")
	for _, p := range a.rfxLoader.Packs() {
		if p.Pack == name && p.Panel != "" {
			data, err := os.ReadFile(p.Panel)
			if err != nil || len(data) > rfx.MaxPanelBytes {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy",
				"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'")
			w.Write([]byte(panelBridge))
			w.Write(data)
			return
		}
	}
	http.NotFound(w, r)
}
