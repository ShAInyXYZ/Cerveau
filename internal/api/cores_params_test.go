package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/config"
)

func paramsFixture(t *testing.T) *API {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CRV_CORES_JSON", filepath.Join(dir, "cores.json"))
	t.Setenv("CRV_CORES_D", filepath.Join(dir, "cores.d"))
	os.WriteFile(filepath.Join(dir, "cores.json"), []byte(`{"active":"bf16","cores":[
	  {"id":"bf16","name":"vLLM · BF16","endpoint":"http://localhost:18030","engine":"vLLM",
	   "params":{"KV":"bf16","VISION":"1","GPU_UTIL":"0.88"}}]}`), 0o644)
	return New(config.Default(), nil)
}

func call(t *testing.T, a *API, method, id, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/cores/"+id+"/params", strings.NewReader(body))
	req.SetPathValue("id", id)
	if method == http.MethodPut {
		a.SetCoreParams(rec, req)
	} else {
		a.CoreParams(rec, req)
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// The panel shows the profile's defaults, and a user's change lands in the
// overrides file as a DIFF: values equal to the default are not written.
func TestCoreParamsShowDefaultsAndSaveOnlyTheDiff(t *testing.T) {
	a := paramsFixture(t)
	code, out := call(t, a, http.MethodGet, "bf16", "")
	if code != 200 || out["defaults"].(map[string]any)["KV"] != "bf16" {
		t.Fatalf("GET: %d %v", code, out)
	}
	code, out = call(t, a, http.MethodPut, "bf16", `{"overrides":{"KV":"fp8","VISION":"1","MAX_PIXELS":"4000000"}}`)
	if code != 200 {
		t.Fatalf("PUT: %d %v", code, out)
	}
	ov := out["overrides"].(map[string]any)
	if ov["KV"] != "fp8" || ov["MAX_PIXELS"] != "4000000" || ov["VISION"] != nil {
		t.Fatalf("overrides should be the diff only: %v", ov)
	}
	eff := out["effective"].(map[string]any)
	if eff["KV"] != "fp8" || eff["VISION"] != "1" || eff["GPU_UTIL"] != "0.88" {
		t.Fatalf("effective wrong: %v", eff)
	}
	raw, err := os.ReadFile(out["file"].(string))
	if err != nil || !strings.Contains(string(raw), `KV="fp8"`) {
		t.Fatalf("env file not written: %v %s", err, raw)
	}
}

// PORT is refused and an unknown Core is a 404 — neither writes anything.
func TestCoreParamsRefusals(t *testing.T) {
	a := paramsFixture(t)
	if code, _ := call(t, a, http.MethodPut, "bf16", `{"overrides":{"PORT":"1"}}`); code != 400 {
		t.Fatalf("PORT should be refused, got %d", code)
	}
	if code, _ := call(t, a, http.MethodGet, "nope", ""); code != 404 {
		t.Fatalf("unknown core should 404, got %d", code)
	}
}
