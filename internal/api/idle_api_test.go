package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cerveau/internal/idle"
)

func withIdle(t *testing.T) (*API, *idle.Tracker, string) {
	t.Helper()
	req := filepath.Join(t.TempDir(), "park.json")
	tr := idle.New(idle.Config{After: time.Hour, Warn: 15 * time.Minute, Enabled: true}, req)
	a := &API{}
	a.SetIdle(tr)
	return a, tr, req
}

func TestIdleStatusEndpoint(t *testing.T) {
	a, _, _ := withIdle(t)
	rec := httptest.NewRecorder()
	a.IdleStatus(rec, httptest.NewRequest("GET", "/api/idle", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var s idle.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s.State != idle.Active || !s.Enabled {
		t.Fatalf("got state=%q enabled=%v", s.State, s.Enabled)
	}
}

func TestIdleNowWritesRequest(t *testing.T) {
	a, _, reqPath := withIdle(t)
	rec := httptest.NewRecorder()
	a.IdleNow(rec, httptest.NewRequest("POST", "/api/idle/now", strings.NewReader("{}")))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body)
	}
	// The park must reach the watchdog as a file — that IS the interface.
	if _, err := readFileEventually(reqPath); err != nil {
		t.Fatalf("park request not written: %v", err)
	}
}

func TestIdleStayClearsPendingRequest(t *testing.T) {
	a, tr, reqPath := withIdle(t)
	if err := tr.ParkNow(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := readFileEventually(reqPath); err != nil {
		t.Fatalf("precondition: %v", err)
	}

	rec := httptest.NewRecorder()
	a.IdleStay(rec, httptest.NewRequest("POST", "/api/idle/stay", strings.NewReader(`{"minutes":30}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	// "Stay awake" has to revoke a park already asked for, or answering the
	// warning a second too late still parks the Core.
	if _, err := readFileEventually(reqPath); err == nil {
		t.Fatal("park request survived a stay-awake")
	}
}

func TestIdleConfigRejectsWarnLongerThanTimeout(t *testing.T) {
	a, tr, _ := withIdle(t)
	rec := httptest.NewRecorder()
	body := `{"after_minutes":10,"warn_minutes":99}`
	a.IdleConfig(rec, httptest.NewRequest("POST", "/api/idle/config", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	c := tr.Cfg()
	if c.Warn >= c.After {
		t.Fatalf("warn %v >= after %v — screen would be up from the first second", c.Warn, c.After)
	}
}

func TestIdleEndpointsSafeWhenUnwired(t *testing.T) {
	a := &API{} // no tracker
	rec := httptest.NewRecorder()
	a.IdleStatus(rec, httptest.NewRequest("GET", "/api/idle", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status should degrade gracefully, got %d", rec.Code)
	}
}

func readFileEventually(p string) ([]byte, error) {
	var err error
	var b []byte
	for i := 0; i < 20; i++ {
		b, err = os.ReadFile(p)
		if err == nil {
			return b, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil, err
}
