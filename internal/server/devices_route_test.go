package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// /api/devices was handled only inside the auth middleware, AFTER the full
// token+signature proof. A loopback request short-circuits above that point —
// so on the machine running Cerveau, listing devices 404'd. The desktop panel
// could never show the fleet, which is exactly where you manage it from.
func TestDevicesRouteIsReachableFromLoopback(t *testing.T) {
	withTempHome(t)
	registerDevice("dev-x", "K1")

	mux := http.NewServeMux()
	registerDeviceRoutes(mux, noTokenCfg{})

	req := httptest.NewRequest("GET", "/api/devices", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatal("/api/devices is not routed — the panel cannot list devices")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !contains(body, "dev-x") {
		t.Errorf("the paired device is missing from the list: %s", body)
	}
}

// authCfg is an interface; the list endpoint only reads the token to decide
// nothing here, so a stub with no token is enough.
type noTokenCfg struct{}

func (noTokenCfg) RemoteToken() string         { return "" }
func (noTokenCfg) SetRemoteToken(string) error { return nil }

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
