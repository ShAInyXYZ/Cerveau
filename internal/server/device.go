package server

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Device identity: after pairing, the phone registers a P-256 public key
// (private key lives in its TEE, never leaves). Each authenticated request
// then carries X-Cerveau-Device (a device id), X-Cerveau-Nonce (a server
// challenge from /api/nonce), and X-Cerveau-Sig = ECDSA-SHA256(nonce).
// The bearer token alone is no longer enough — a stolen token is dead
// without the phone's secure element.

const devicesPath = "devices.json"

func devicesFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".crv", devicesPath)
}

type device struct {
	ID      string `json:"id"`
	PubKey  string `json:"pubkey"` // base64 SPKI
	AddedAt string `json:"added_at"`
}

var (
	devMu   sync.Mutex
	nonceMu sync.Mutex
	nonces  = map[string]time.Time{} // nonce → expiry
)

func loadDevices() []device {
	data, err := os.ReadFile(devicesFile())
	if err != nil {
		return nil
	}
	var out []device
	_ = json.Unmarshal(data, &out)
	return out
}

func saveDevices(ds []device) error {
	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(devicesFile(), data, 0o600)
}

func registerDevice(id, pubkeyB64 string) error {
	devMu.Lock()
	defer devMu.Unlock()
	ds := loadDevices()
	for _, d := range ds {
		if d.ID == id {
			d.PubKey = pubkeyB64 // re-pair replaces
			return saveDevices(ds)
		}
	}
	ds = append(ds, device{ID: id, PubKey: pubkeyB64, AddedAt: time.Now().UTC().Format(time.RFC3339)})
	return saveDevices(ds)
}

func findDevice(id string) *device {
	devMu.Lock()
	defer devMu.Unlock()
	for _, d := range loadDevices() {
		if d.ID == id {
			cp := d
			return &cp
		}
	}
	return nil
}

func verifyDeviceSig(id, nonceB64, sigB64 string) bool {
	d := findDevice(id)
	if d == nil {
		return false
	}
	nonceMu.Lock()
	exp, ok := nonces[nonceB64]
	delete(nonces, nonceB64) // one-shot: a nonce can never be replayed
	nonceMu.Unlock()
	if !ok || time.Now().After(exp) {
		return false
	}
	pubDER, err := base64.StdEncoding.DecodeString(d.PubKey)
	if err != nil {
		return false
	}
	pubAny, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		return false
	}
	pub, ok := pubAny.(*ecdsa.PublicKey)
	if !ok {
		return false
	}
	sigDER, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	nonce, _ := base64.StdEncoding.DecodeString(nonceB64)
	h := sha256.Sum256(nonce)
	// parse ASN.1 ECDSA signature
	r, s, ok2 := parseASN1ECDSA(sigDER)
	if !ok2 {
		return false
	}
	return ecdsa.Verify(pub, h[:], r, s)
}

// parseASN1ECDSA pulls r,s out of a DER ECDSA signature using encoding/asn1.
func parseASN1ECDSA(der []byte) (*big.Int, *big.Int, bool) {
	type sig struct{ R, S *big.Int }
	var out sig
	if _, err := asn1.Unmarshal(der, &out); err != nil {
		return nil, nil, false
	}
	return out.R, out.S, out.R != nil && out.S != nil
}

// serveNonce hands out a short-lived challenge for device signing.
func serveNonce(w http.ResponseWriter) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	n := base64.StdEncoding.EncodeToString(b[:])
	nonceMu.Lock()
	nonces[n] = time.Now().Add(30 * time.Second)
	// GC old nonces
	for k, exp := range nonces {
		if time.Now().After(exp) {
			delete(nonces, k)
		}
	}
	nonceMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"nonce":%q}`, n)
}

// newDeviceID derives a stable device id from the public key so the phone
// doesn't need to store one separately.
func newDeviceID(pubkeyB64 string) string {
	pubDER, err := base64.StdEncoding.DecodeString(pubkeyB64)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(pubDER)
	return hex.EncodeToString(h[:8])
}

// isDeviceAuth reports whether the request carries a valid device signature.
func isDeviceAuth(r *http.Request) bool {
	id := r.Header.Get("X-Cerveau-Device")
	nonce := r.Header.Get("X-Cerveau-Nonce")
	sig := r.Header.Get("X-Cerveau-Sig")
	if id == "" || nonce == "" || sig == "" {
		return false
	}
	return verifyDeviceSig(id, nonce, sig)
}

func deviceRegistered(id string) bool { return findDevice(id) != nil }
