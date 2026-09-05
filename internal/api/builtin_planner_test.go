package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"cerveau/internal/rfx"
)

func TestBuiltinPlannerHTTPIdentityAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "planner")
	if err := os.MkdirAll(filepath.Join(installed, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte("rfx: 1\npack: planner\nversion: 0.1.0\ndescription: stale\n")
	panel := []byte("<p>STALE PLANNER MUST NOT LOAD</p>")
	if err := os.WriteFile(filepath.Join(installed, "pack.yaml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "ui", "panel.html"), panel, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &API{rfxLoader: rfx.NewLoader(dir, nil, rfx.WithBuiltinPlanner())}
	builtin, err := rfx.BuiltinPlanner()
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	a.ListRfx(res, httptest.NewRequest("GET", "/api/rfx", nil))
	var list struct {
		Packs []struct {
			Name, Version, Origin string
			ContentSHA256         string                     `json:"content_sha256"`
			IgnoredInstalled      []rfx.IgnoredInstalledPack `json:"ignored_installed"`
			HasPanel              bool                       `json:"has_panel"`
			UIOnly                bool                       `json:"ui_only"`
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Packs) != 1 || list.Packs[0].Name != "planner" || list.Packs[0].Origin != "builtin" || list.Packs[0].ContentSHA256 != builtin.ContentSHA256 || !list.Packs[0].HasPanel || !list.Packs[0].UIOnly || len(list.Packs[0].IgnoredInstalled) != 1 {
		t.Fatalf("pack identity: %s", res.Body.String())
	}
	res = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/rfx/panel/planner", nil)
	req.SetPathValue("pack", "planner")
	a.PanelRfx(res, req)
	want, err := builtin.ReadPanel()
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != http.StatusOK || !bytes.Equal(res.Body.Bytes(), append([]byte(panelBridge), want...)) || res.Header().Get("Cache-Control") != "no-store" || res.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("wrong panel or headers: %d %+v", res.Code, res.Header())
	}
	res = httptest.NewRecorder()
	a.Build(res, httptest.NewRequest("GET", "/api/build", nil))
	var info BuildInfo
	if err := json.Unmarshal(res.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Planner.Loaded || info.Planner.Name != "planner" || info.Planner.Version != builtin.Version || info.Planner.Origin != "builtin" || info.Planner.ContentSHA256 != builtin.ContentSHA256 || info.Planner.Precedence != "builtin-wins" || len(info.Planner.IgnoredInstalled) != 1 || info.Planner.IgnoredInstalled[0].Path != installed {
		t.Fatalf("build identity: %s", res.Body.String())
	}
	for name, want := range map[string][]byte{"pack.yaml": manifest, "ui/panel.html": panel} {
		got, err := os.ReadFile(filepath.Join(installed, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("installed file mutated: %s %v", name, err)
		}
	}
}

func TestBuildDoesNotClaimUnavailablePlannerLoaded(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "planner", "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "planner", "pack.yaml"), []byte("rfx: 1\npack: planner\nversion: 0.1.0\ndescription: external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "planner", "ui", "panel.html"), []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, loader := range []*rfx.Loader{nil, rfx.NewLoader(dir, nil)} {
		a := &API{rfxLoader: loader}
		res := httptest.NewRecorder()
		a.Build(res, httptest.NewRequest("GET", "/api/build", nil))
		var info BuildInfo
		if err := json.Unmarshal(res.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		if info.Planner.Loaded || info.Planner.Origin != "builtin" || info.Planner.ContentSHA256 == "" || len(info.Planner.IgnoredInstalled) != 0 {
			t.Fatalf("unavailable bundle misreported: %s", res.Body.String())
		}
	}
	if info := BinaryBuildInfo(); info.Planner.Loaded || info.Planner.Version == "" || info.Planner.ContentSHA256 == "" {
		t.Fatalf("binary-only identity: %+v", info)
	}
}

func TestExternalRfxPanelStillServesInstalledContentAlongsideBuiltinPlanner(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "custom", "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom", "pack.yaml"), []byte("rfx: 1\npack: custom\nversion: 1.0.0\ndescription: external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom", "ui", "panel.html"), []byte("<p>Independent pack</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &API{rfxLoader: rfx.NewLoader(dir, nil, rfx.WithBuiltinPlanner())}
	res := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/rfx/panel/custom", nil)
	req.SetPathValue("pack", "custom")
	a.PanelRfx(res, req)
	if res.Code != http.StatusOK || res.Body.String() != panelBridge+"<p>Independent pack</p>" {
		t.Fatalf("external panel regressed: %d", res.Code)
	}
}
