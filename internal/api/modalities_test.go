package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestModalitiesDistinguishReportedFalseFromUnknown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   map[string]bool
	}{
		{"explicit true", 200, `{"modalities":{"vision":true}}`, map[string]bool{"text": true, "vision": true}},
		{"explicit false", 200, `{"modalities":{"vision":false,"audio":false}}`, map[string]bool{"text": true, "vision": false, "audio": false}},
		{"missing", 200, `{"model":"vision-capable-name-is-not-proof"}`, map[string]bool{"text": true}},
		{"empty", 200, `{"modalities":{}}`, map[string]bool{"text": true}},
		{"null not false", 200, `{"modalities":{"vision":null}}`, map[string]bool{"text": true}},
		{"wrong type", 200, `{"modalities":{"vision":"false","audio":true}}`, map[string]bool{"text": true, "audio": true}},
		{"not found", 404, `{"modalities":{"vision":false}}`, map[string]bool{"text": true}},
		{"unavailable", 503, `{"modalities":{"vision":true}}`, map[string]bool{"text": true}},
		{"malformed", 200, `{"modalities":`, map[string]bool{"text": true}},
		{"unknown key", 200, `{"modalities":{"vision":true,"arbitrary":true}}`, map[string]bool{"text": true, "vision": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/props" {
					t.Error(r.URL.Path)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			a := &API{http: server.Client()}
			if got := a.probeModalities(server.URL); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestModalitiesInvalidOrUnreachableEndpointIsUnknown(t *testing.T) {
	a := &API{http: &http.Client{}}
	for _, base := range []string{"://invalid", "http://127.0.0.1:1"} {
		got := a.probeModalities(base)
		if _, known := got["vision"]; known {
			t.Fatalf("failure reported as known capability: %v", got)
		}
	}
}
