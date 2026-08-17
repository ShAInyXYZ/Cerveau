package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// Pairing by invitation.
//
// Typing a full tailnet hostname on a phone is miserable, and compiling that
// hostname into the APK leaks the user's network to anyone who unzips it. So
// the machine mints a SHORT-LIVED INVITATION instead:
//
//	https://<gate>/p/K3M9   →  a page showing a QR + the 6-char code
//
// The phone scans the QR (which carries the gate origin AND the code) or the
// user types the 6 characters. Either way the app learns where to pair from
// the invitation itself — nothing about the network ships in the binary.
//
// Safe to put the address in the QR: the page is only reachable over the
// tailnet, so anyone who can read it already has network access. The
// invitation expires, and its code is one-shot.
const (
	pairTTL = 5 * time.Minute
	// no O/0/I/1/L — a code is read off a screen and typed on a phone
	pairAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

type invitation struct {
	Slug    string // 4 chars, the URL suffix
	Code    string // 6 chars, what actually authorizes
	Gate    string // origin the phone should talk to
	expires time.Time
}

// QRPayload is what the QR encodes: a compact, unambiguous pairing blob.
func (i invitation) QRPayload() string {
	return fmt.Sprintf("cerveau://pair?gate=%s&code=%s", i.Gate, i.Code)
}

type pairSessions struct {
	mu    sync.Mutex
	items map[string]*invitation // by code
	slugs map[string]string      // slug -> code
}

func newPairSessions() *pairSessions {
	return &pairSessions{
		items: map[string]*invitation{},
		slugs: map[string]string{},
	}
}

func randomFrom(alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			// crypto/rand failing is fatal for a security token; refuse to
			// emit a weak one
			return ""
		}
		b[i] = alphabet[k.Int64()]
	}
	return string(b)
}

// mint creates a fresh invitation for the given gate origin.
func (s *pairSessions) mint(gate string) invitation {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	inv := &invitation{
		Slug:    randomFrom(pairAlphabet, 4),
		Code:    randomFrom(pairAlphabet, 6),
		Gate:    gate,
		expires: time.Now().Add(pairTTL),
	}
	s.items[inv.Code] = inv
	s.slugs[inv.Slug] = inv.Code
	return *inv
}

// bySlug resolves a URL slug to its live invitation.
func (s *pairSessions) bySlug(slug string) (invitation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	code, ok := s.slugs[strings.ToUpper(slug)]
	if !ok {
		return invitation{}, false
	}
	inv, ok := s.items[code]
	if !ok || time.Now().After(inv.expires) {
		return invitation{}, false
	}
	return *inv, true
}

// consume authorizes a pairing exactly once.
func (s *pairSessions) consume(code string) (invitation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	inv, ok := s.items[strings.ToUpper(code)]
	if !ok || time.Now().After(inv.expires) {
		return invitation{}, false
	}
	delete(s.items, inv.Code)
	delete(s.slugs, inv.Slug)
	return *inv, true
}

func (s *pairSessions) gcLocked() {
	now := time.Now()
	for code, inv := range s.items {
		if now.After(inv.expires) {
			delete(s.items, code)
			delete(s.slugs, inv.Slug)
		}
	}
}

// ---- HTTP ----

// servePairPortal renders the "pair a phone" page: mints an invitation and
// shows the QR + code. Reachable only from the machine itself or over the
// tailnet (the auth gate leaves non-/api paths open so this can render).
func (s *pairSessions) servePairPortal(w http.ResponseWriter, r *http.Request, gate string) {
	inv := s.mint(gate)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	png, err := qrcode.Encode(inv.QRPayload(), qrcode.Medium, 320)
	qrImg := ""
	if err == nil {
		qrImg = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	fmt.Fprintf(w, pairPageHTML,
		inv.Code, qrImg, int(pairTTL.Seconds()), gate, inv.Slug)
}

// The page draws its own QR client-side from the payload — no image
// dependency on the server, and the payload never leaves the tailnet.
const pairPageHTML = `<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Pair a phone — Cerveau</title>
<style>
  :root { color-scheme: dark; }
  body { margin:0; min-height:100vh; display:grid; place-items:center;
         background:#09090B; color:#FAFAFA;
         font:14px/1.5 ui-sans-serif,system-ui,sans-serif; }
  .card { text-align:center; padding:32px 28px; }
  h1 { font-size:15px; font-weight:600; letter-spacing:.18em; text-transform:uppercase;
       color:#A1A1AA; margin:0 0 22px; }
  .code { font:600 40px/1 ui-monospace,SFMono-Regular,monospace;
          letter-spacing:.28em; color:#E54866; margin:0 0 6px; padding-left:.28em; }
  .sub { color:#71717A; font-size:12.5px; margin-bottom:24px; }
  #qr { background:#fff; padding:12px; border-radius:10px; display:inline-block; }
  .exp { margin-top:20px; color:#52525B; font-size:11.5px; }
  .url { margin-top:10px; color:#71717A; font-size:11.5px;
         font-family:ui-monospace,monospace; }
</style>
<div class="card">
  <h1>Pair a phone</h1>
  <div class="code">%s</div>
  <div class="sub">scan this, or type the code in the app</div>
  <div id="qr"><img src="%s" width="240" height="240" alt="pairing QR"></div>
  <div class="exp">expires in <span id="t">%d</span>s · one use</div>
  <div class="url">%s/p/%s</div>
</div>
<script>
  // local countdown only — the server holds the real expiry
  let t = document.getElementById('t');
  let n = parseInt(t.textContent, 10);
  setInterval(() => { n = Math.max(0, n - 1); t.textContent = n;
    if (n === 0) location.reload(); }, 1000);
</script>
`

// gateOrigin reconstructs the origin the CLIENT used, so the QR points back
// through the same door the browser came in by (the NAS gate when proxied,
// the machine directly when local) — never a compiled-in address.
func gateOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

// servePairInvite re-renders an existing invitation (following /p/<slug>)
// without minting a new one.
func servePairInvite(w http.ResponseWriter, inv invitation) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	png, err := qrcode.Encode(inv.QRPayload(), qrcode.Medium, 320)
	qrImg := ""
	if err == nil {
		qrImg = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	remaining := int(time.Until(inv.expires).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	fmt.Fprintf(w, pairPageHTML, inv.Code, qrImg, remaining, inv.Gate, inv.Slug)
}
