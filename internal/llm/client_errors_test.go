package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientPreservesStatusAndErrorShapes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"string authentication error", 401, `{"error":"Unauthorized"}`, "Unauthorized"},
		{"OpenAI object", 404, `{"error":{"message":"The model local does not exist","type":"NotFoundError"}}`, "The model local does not exist"},
		{"top-level message", 503, `{"message":"Core is unavailable"}`, "Core is unavailable"},
		{"FastAPI detail", 400, `{"detail":"Missing model"}`, "Missing model"},
		{"FastAPI validation list", 422, `{"detail":[{"msg":"field required","loc":["body","model"]}]}`, "field required"},
		{"plain text proxy", 502, "upstream connection failed", "upstream connection failed"},
		{"HTML proxy", 503, "<html>upstream unavailable</html>", "upstream unavailable"},
		{"empty error body", 401, "", "Unauthorized"},
		{"top-level JSON string", 429, `"rate limited"`, "rate limited"},
		{"error without message", 500, `{"error":{"type":"InternalError","code":17}}`, "InternalError"},
		{"error envelope on success status", 200, `{"error":"server rejected request"}`, "server rejected request"},
		{"object error on success status", 200, `{"error":{"message":"bad request"}}`, "bad request"},
		{"non-success choices must fail", 503, `{"choices":[{"message":{"role":"assistant","content":"not a successful response"}}]}`, "choices"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			client := NewClient(srv.URL)
			_, _, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "fixture"}}, nil, "", 1)
			var statusError *HTTPError
			if !errors.As(err, &statusError) || statusError.StatusCode != tc.status || !strings.Contains(statusError.Message, tc.want) {
				t.Fatalf("expected HTTP %d with %q, got %v", tc.status, tc.want, err)
			}
			if strings.Contains(err.Error(), "parse llm response") {
				t.Fatalf("error shape hid the actual upstream failure: %v", err)
			}
		})
	}
}

func TestClientSuccessfulResponseAllowsAbsentOrNullError(t *testing.T) {
	for _, errorField := range []string{"", `,"error":null`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":4,"completion_tokens":1}` + errorField + `}`))
		}))
		client := NewClient(srv.URL)
		msg, usage, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "fixture"}}, nil, "", 1)
		srv.Close()
		if err != nil || msg.Content != "ok" || usage.PromptTokens != 4 {
			t.Fatalf("valid success changed: msg=%+v usage=%+v err=%v", msg, usage, err)
		}
	}
}

func TestClientUsesConfiguredModelAndAuthWithoutLeakingCredential(t *testing.T) {
	const key = "test-only-private-bearer-token"
	t.Setenv("CRV_MODEL_NAME", "rig-model")
	t.Setenv("CRV_MODEL_KEY", key)
	var model, authorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		model, authorization = request.Model, r.Header.Get("Authorization")
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Rejected bearer " + key})
	}))
	defer srv.Close()
	client := NewClient(srv.URL)
	_, _, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "fixture"}}, nil, "", 1)
	if model != "rig-model" || authorization != "Bearer "+key {
		t.Fatal("configured model/auth did not reach the request")
	}
	if err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("credential echoed into diagnostic: %v", err)
	}
}

func TestClientBoundsUpstreamErrorDiagnostics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte(strings.Repeat("x", 100000)))
	}))
	defer srv.Close()
	client := NewClient(srv.URL)
	_, _, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "fixture"}}, nil, "", 1)
	var statusError *HTTPError
	if !errors.As(err, &statusError) || len(statusError.Message) > 2051 {
		t.Fatalf("unbounded or missing upstream diagnostic: %v", err)
	}
}
