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
// proof of physical access). The phone app trades it at /api/pair for the
// long-lived access token; every other route then requires
// "Authorization: Bearer <token>". Pair IDs are one-shot and rate-limited
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
	mu      sync.Mutex
	count   int
	window  time.Time
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

// authGate enforces the bearer token on every route except /api/pair and
// /api/health (health is unauthenticated so probes stay trivial; it leaks
// nothing beyond "cerveau is alive"). When no token exists yet (fresh
// localhost-only setup), everything is open — pre-auth behavior — and
// /api/pair mints the first token.
func authGate(cfg authCfg, inner http.Handler) http.Handler {
	limiter := &pairLimiter{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pair" {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			servePair(cfg, limiter, w, r)
			return
		}
		token := cfg.RemoteToken()
		if token == "" {
			inner.ServeHTTP(w, r) // unpaired localhost setup — same as before auth existed
			return
		}
		if r.URL.Path == "/api/health" {
			inner.ServeHTTP(w, r) // "is it alive" is not a secret
			return
		}
		// Static UI assets (panel JS/CSS/icons/manifest) carry no session
		// data and must load so the panel can show its lock screen and the
		// phone can offer installability. Everything under /api/ stays gated.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			inner.ServeHTTP(w, r)
			return
		}
		if strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func servePair(cfg authCfg, limiter *pairLimiter, w http.ResponseWriter, r *http.Request) {
	if !limiter.allow() {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	var body struct {
		PairID string `json:"pair_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.PairID) != 6 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if cfg.RemoteToken() != "" {
		http.Error(w, "already paired", http.StatusConflict)
		return
	}
	id, err := EnsurePairID()
	if err != nil || body.PairID != id {
		http.Error(w, "wrong pairing id", http.StatusUnauthorized)
		return
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
	_ = os.Remove(pairIDFile()) // one-shot: no pairing twice from the same ID
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"token":%q}`, token)
}
