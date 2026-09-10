package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"cerveau/internal/rfx"
	"cerveau/internal/tools"
)

func TestErrorEnvelopeIsBoundedAfterJSONEscaping(t *testing.T) {
	for _, body := range []string{strings.Repeat("&<\x00", 12000), strings.Repeat("界", 12000)} {
		envelope := errorEnvelope(errors.New("engine_error: " + body))
		encoded, err := json.Marshal(envelope)
		if err != nil || len(encoded) > 24000 {
			t.Fatalf("unbounded serialized error: %d (%v)", len(encoded), err)
		}
		details := envelope["error"].(map[string]any)
		if details["code"] != "engine_error" || details["truncated"] != true || !utf8.ValidString(details["message"].(string)) {
			t.Fatalf("bad error envelope: %#v", details)
		}
	}
}

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "rfx-dgv" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestManifestRegistryExecComposition(t *testing.T) {
	core := os.Getenv("RFX_DGV_CORE")
	if core == "" {
		core = filepath.Join("..", "..", "..", "Dia-GramV", "packages", "core", "src", "index.js")
	}
	core, _ = filepath.Abs(core)
	if _, err := os.Stat(core); err != nil {
		t.Skip("original DGV core unavailable; set RFX_DGV_CORE")
	}
	ws, bin := t.TempDir(), t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(bin, "rfx-dgv")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RFX_DGV_CORE", core)
	files, err := filepath.Glob(filepath.Join("..", "..", "rfx", "dgv", "*.rfx.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var defs []rfx.Reflex
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		def, err := rfx.Parse(b, file)
		if err != nil {
			t.Fatal(err)
		}
		if err := rfx.Validate(def, nil); err != nil {
			t.Fatal(err)
		}
		defs = append(defs, *def)
	}
	reg := tools.NewRegistry()
	reg.SetWorkspace(ws)
	if errs := reg.AddReflexes(defs); len(errs) > 0 {
		t.Fatal(errs)
	}
	create := json.RawMessage(`{"name":"fixture","document":{"dgv":1,"meta":{"title":"Registry composition"},"frames":[],"nodes":[{"id":"app","kind":"program","label":"App","path":"missing.go"}],"edges":[]}}`)
	if _, err := reg.ExecuteMode(context.Background(), "dgv-create", create, tools.ModeDiscussion); err == nil {
		t.Fatal("discussion mutation was allowed")
	}
	if out, err := reg.ExecuteMode(context.Background(), "dgv-create", create, tools.ModeAutopilot); err != nil {
		t.Fatalf("manifest create: %s (%v)", out, err)
	}
	if out, err := reg.ExecuteMode(context.Background(), "dgv-context", json.RawMessage(`{"name":"fixture"}`), tools.ModeDiscussion); err != nil {
		t.Fatalf("manifest context: %s (%v)", out, err)
	}
	if out, err := reg.ExecuteMode(context.Background(), "dgv-update", json.RawMessage(`{"name":"fixture","expected_hash":"bad","patch":{"nodes":[{"id":"app","status":"done"}]}}`), tools.ModeAutopilot); err == nil {
		t.Fatalf("invalid update was accepted: %s", out)
	}
	out, err := reg.ExecuteMode(context.Background(), "dgv-check", json.RawMessage(`{"name":"fixture","checks":"drift"}`), tools.ModeDiscussion)
	if err == nil {
		t.Fatal("failed check recorded as successful through actual manifest registry exec path")
	}
	var result map[string]any
	if parseErr := json.Unmarshal([]byte(out), &result); parseErr != nil || result["ok"] != false || result["drift"] == nil {
		t.Fatalf("lost structured failure through exec: %s (%v)", out, parseErr)
	}
}

func TestDGVCLIProcess(t *testing.T) {
	if os.Getenv("RFX_DGV_CLI_TEST") != "1" {
		return
	}
	os.Args = []string{"rfx-dgv", "check"}
	main()
	os.Exit(0)
}

func TestFailedCheckExitsNonzeroWithOneStructuredResult(t *testing.T) {
	core := os.Getenv("RFX_DGV_CORE")
	if core == "" {
		core = filepath.Join("..", "..", "..", "Dia-GramV", "packages", "core", "src", "index.js")
	}
	core, _ = filepath.Abs(core)
	if _, err := os.Stat(core); err != nil {
		t.Skip("original DGV core unavailable; set RFX_DGV_CORE")
	}
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "dgv"), 0700); err != nil {
		t.Fatal(err)
	}
	doc := `{"dgv":1,"meta":{"title":"Missing implementation"},"nodes":[{"id":"app","kind":"program","label":"App","path":"missing.go"}],"edges":[]}`
	if err := os.WriteFile(filepath.Join(ws, "dgv", "fixture.dgv.json"), []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestDGVCLIProcess$")
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "RFX_DGV_CLI_TEST=1", "RFX_DGV_CORE="+core)
	cmd.Stdin = bytes.NewBufferString(`{"name":"fixture","checks":"drift"}`)
	raw, err := cmd.Output()
	if err == nil {
		t.Fatal("failed drift check exited successfully")
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("expected one complete JSON result: %s (%v)", raw, err)
	}
	if result["ok"] != false || result["drift"] == nil || result["graph_sha256"] == nil {
		t.Fatalf("lost diagnostic evidence: %s", raw)
	}
}
