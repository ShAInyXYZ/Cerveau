package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"cerveau/internal/config"
	"cerveau/internal/idle"
)

type idleHealthTransport func(*http.Request) (*http.Response, error)

func (f idleHealthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubIdleHealth(a *API, modelRequests *[]string) {
	a.cfg = config.Default()
	a.cfg.Endpoints = config.Endpoints{Model: "http://model.invalid", Embedder: "http://embedder.invalid", Typesense: "http://index.invalid"}
	a.http = &http.Client{Transport: idleHealthTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "model.invalid" {
			*modelRequests = append(*modelRequests, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"fixture"}],"modalities":{"vision":true}}`))}, nil
	})}
}

func TestRefreshIdleReturnsObservationWithoutDiscardingLastDisplay(t *testing.T) {
	a, tracker, _ := withIdle(t)
	tracker.Observe(idle.Active)
	a.idleCoreState = func(context.Context) idle.State { return "" }
	if observed := a.RefreshIdle(context.Background()); observed != "" {
		t.Fatalf("unknown metadata became stale observed state %q", observed)
	}
	if display := tracker.Status().State; display != idle.Active {
		t.Fatalf("transient unknown metadata discarded the display state: %q", display)
	}
}

func TestIdleHealthRequiresFreshActiveObservation(t *testing.T) {
	for _, test := range []struct {
		name                  string
		previous, observation idle.State
		display               idle.State
		probe                 bool
	}{
		{"previously active now unknown", idle.Active, "", idle.Active, false},
		{"previously parked now unknown", idle.Parked, "", idle.Parked, false},
		{"parked", idle.Active, idle.Parked, idle.Parked, false},
		{"waking", idle.Active, idle.Waking, idle.Waking, false},
		{"unavailable", idle.Active, idle.Unavailable, idle.Unavailable, false},
		{"known active", idle.Parked, idle.Active, idle.Active, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, tracker, _ := withIdle(t)
			tracker.Observe(test.previous)
			a.idleCoreState = func(context.Context) idle.State { return test.observation }
			var requests []string
			stubIdleHealth(a, &requests)
			rec := httptest.NewRecorder()
			a.Health(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("health status=%d", rec.Code)
			}
			if test.probe {
				if !reflect.DeepEqual(requests, []string{"/health", "/v1/models", "/props"}) {
					t.Fatalf("fresh active Core probes changed: %q", requests)
				}
			} else if len(requests) != 0 {
				t.Fatalf("non-active/unknown metadata caused socket-capable model probes: %q", requests)
			}
			var body struct {
				Components []ComponentStatus `json:"components"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Components) != 3 {
				t.Fatalf("health response=%s err=%v", rec.Body.String(), err)
			}
			if body.Components[0].OK != test.probe {
				t.Fatalf("model health claimed an unobserved success: %+v", body.Components[0])
			}
			if test.observation == "" && !strings.Contains(body.Components[0].Detail, "unknown") {
				t.Fatalf("missing metadata was not explained: %+v", body.Components[0])
			}
			if display := tracker.Status().State; display != test.display {
				t.Fatalf("tracker display=%q want=%q", display, test.display)
			}
		})
	}
}

func TestIdleUnwiredHealthPreservesUnmanagedProbes(t *testing.T) {
	a := &API{}
	var requests []string
	stubIdleHealth(a, &requests)
	a.Health(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if !reflect.DeepEqual(requests, []string{"/health", "/v1/models", "/props"}) {
		t.Fatalf("unwired unmanaged deployment stopped normal probes: %q", requests)
	}
}
