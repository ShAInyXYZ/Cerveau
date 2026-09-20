package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func rigCall(t *testing.T, h http.HandlerFunc, method, url, body string, json_ bool) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	if json_ {
		req.Header.Set("Content-Type", "application/json")
	}
	h(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A profile that does not exist is refused before the driver is asked anything.
func TestRigRefusesAnUnknownProfile(t *testing.T) {
	a := paramsFixture(t)
	if code, out := rigCall(t, a.Rig, http.MethodGet, "/api/rig?core=nope", "", false); code != http.StatusNotFound {
		t.Fatalf("want 404, got %d %v", code, out)
	}
	if code, _ := rigCall(t, a.RigPlan, http.MethodPost, "/api/rig/plan", `{"core":"nope","gpus":[0]}`, true); code != http.StatusNotFound {
		t.Fatalf("plan: want 404, got %d", code)
	}
	if code, _ := rigCall(t, a.RigPlan, http.MethodPost, "/api/rig/plan", `{"gpus":[0]}`, true); code != http.StatusBadRequest {
		t.Fatalf("plan without a core: want 400, got %d", code)
	}
}

// "Save as": kept, listed, replaced by name, forgotten — and only over JSON,
// so a sandboxed panel cannot write the file.
func TestRigLayoutsRoundTrip(t *testing.T) {
	a := paramsFixture(t)
	t.Setenv("CRV_RIG_LAYOUTS", filepath.Join(t.TempDir(), "rig-layouts.json"))
	put := `{"core":"bf16","layout":{"name":"pack","gpus":[3,2],"embed":{"device":"cpu"},"gpu_util":0.9}}`

	if code, _ := rigCall(t, a.RigLayouts, http.MethodPut, "/api/rig/layouts", put, false); code != http.StatusUnsupportedMediaType {
		t.Fatalf("a write without a JSON content type must be refused, got %d", code)
	}
	code, out := rigCall(t, a.RigLayouts, http.MethodPut, "/api/rig/layouts", put, true)
	layouts, _ := out["layouts"].([]any)
	if code != http.StatusOK || len(layouts) != 1 {
		t.Fatalf("put: %d %v", code, out)
	}
	if first := layouts[0].(map[string]any); first["name"] != "pack" || first["gpus"].([]any)[0].(float64) != 2 {
		t.Fatalf("layout not normalised: %v", first)
	}
	// another profile keeps its own
	_, other := rigCall(t, a.RigLayouts, http.MethodGet, "/api/rig/layouts?core=w8a16", "", false)
	if got, _ := other["layouts"].([]any); got == nil || len(got) != 0 {
		t.Fatalf("an empty list, not null — the panel maps over it: %v", other)
	}
	_, out = rigCall(t, a.RigLayouts, http.MethodDelete, "/api/rig/layouts?core=bf16&name=pack", "{}", true)
	if got, _ := out["layouts"].([]any); len(got) != 0 {
		t.Fatalf("after delete: %v", out)
	}
}
