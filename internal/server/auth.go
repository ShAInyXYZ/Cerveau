package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Pairing: the core prints a short ID on startup (the machine's console is
// proof of physical access). The phone trades it at /api/pair for a bearer
// token AND registers a device public key in the same call. After that every
// request needs BOTH:
//   Authorization: Bearer <token>               (shared secret)
//   X-Cerveau-Device / X-Cerveau-Nonce / X-Cerveau-Sig
//                                               (this phone's TEE signed a
//                                                fresh server challenge)
// Either half alone is dead. Pair IDs are one-shot and rate-limited
// (5 attempts / minute) so they can't be brute-forced over the wire.

const pairIDPath = "pair.id"

func pairIDFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".crv", pairIDPath)
}

// EnsurePairID returns the persisted pairing ID, minting a new one if none
// exists. Once the access token is set in config this is never called.
func EnsurePairID() (string, error) {
	if data, err := os.ReadFile(pairIDFile()); err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:]) // 6 hex chars — human-typable, brute-force blocked by the rate limit
	if err := os.WriteFile(pairIDFile(), []byte(id), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

type pairLimiter struct {
	mu     sync.Mutex
	count  int
	window time.Time
}

func (l *pairLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.window) > time.Minute {
		l.count, l.window = 0, time.Now()
	}
	l.count++
	return l.count <= 5
}

type authCfg interface {
	RemoteToken() string
	SetRemoteToken(token string) error
}

// authGate enforces token + device-signature on every /api/ route except
// /api/pair, /api/nonce, and /api/health. Static UI assets stay open so the
// panel can render its lock screen. When no token exists yet (fresh
// localhost-only setup), everything is open — pre-auth behavior.
func authGate(cfg authCfg, inner http.Handler) http.Handler {
	limiter := &pairLimiter{}
	invites := newPairSessions()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// The pairing portal: shows a QR + short code so a phone never has to
		// type a tailnet hostname (and the APK never has to contain one).
		// Only reachable by someone already on the network.
		if path == "/pair" || strings.HasPrefix(path, "/p/") {
			gate := gateOrigin(r)
			if strings.HasPrefix(path, "/p/") {
				if inv, ok := invites.bySlug(strings.TrimPrefix(path, "/p/")); ok {
					servePairInvite(w, inv)
					return
				}
				http.Error(w, "this pairing link has expired — open /pair again", http.StatusGone)
				return
			}
			invites.servePairPortal(w, r, gate)
			return
		}
		switch path {
		case "/api/pair":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			servePair(cfg, limiter, invites, w, r)
			return
		case "/api/nonce":
			serveNonce(w)
			return
		case "/api/health":
			inner.ServeHTTP(w, r) // "is it alive" is not a secret
			return
		}

		token := cfg.RemoteToken()
		if token == "" {
			inner.ServeHTTP(w, r) // unpaired localhost setup — same as before auth existed
			return
		}
		// Static UI assets (panel JS/CSS/icons/manifest) carry no session data
		// and must load so the panel can show its lock screen.
		if !strings.HasPrefix(path, "/api/") {
			inner.ServeHTTP(w, r)
			return
		}

		// The full proof: bearer token AND a live device signature.
		if strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !isDeviceAuth(r) {
			http.Error(w, "device not verified", http.StatusForbidden)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func servePair(cfg authCfg, limiter *pairLimiter, invites *pairSessions, w http.ResponseWriter, r *http.Request) {
	if !limiter.allow() {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	var body struct {
		PairID string `json:"pair_id"`
		PubKey string `json:"pubkey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.PairID) != 6 || body.PubKey == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if cfg.RemoteToken() != "" {
		http.Error(w, "already paired", http.StatusConflict)
		return
	}
	// A live invitation code (from /pair) authorizes, one-shot. The
	// console-printed pair.id remains valid as the offline fallback.
	if _, ok := invites.consume(body.PairID); !ok {
		id, err := EnsurePairID()
		if err != nil || !strings.EqualFold(body.PairID, id) {
			http.Error(w, "wrong pairing id", http.StatusUnauthorized)
			return
		}
	}
	var tb [32]byte
	if _, err := rand.Read(tb[:]); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(tb[:])
	if err := cfg.SetRemoteToken(token); err != nil {
		http.Error(w, "persist failed", http.StatusInternalServerError)
		return
	}
	devID := newDeviceID(body.PubKey)
	if devID == "" {
		http.Error(w, "bad pubkey", http.StatusBadRequest)
		return
	}
	if err := registerDevice(devID, body.PubKey); err != nil {
		http.Error(w, "device persist failed", http.StatusInternalServerError)
		return
	}
	_ = os.Remove(pairIDFile()) // one-shot: no pairing twice from the same ID
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"token":%q,"device_id":%q}`, token, devID)
}
