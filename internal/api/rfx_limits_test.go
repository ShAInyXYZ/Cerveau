package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReflexUIRejectsOversizedOrTrailingRequestsBeforeExecution(t *testing.T) {
	f := newRunAPIFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("model must not be called") })
	for _, raw := range []string{strings.Repeat(" ", maxCommandBody+1), `{"name":"read","session_id":"invalid","args":{}} {}`} {
		req := httptest.NewRequest("POST", "/api/rfx/run", strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		f.a.RunRfx(res, req)
		if res.Code != 400 && res.Code != 413 {
			t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
		}
		if strings.Contains(res.Body.String(), "session_id required") {
			t.Fatal("request reached execution admission")
		}
	}
}
