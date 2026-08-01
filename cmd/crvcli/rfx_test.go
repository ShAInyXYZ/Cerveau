package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testReflex = `
rfx: 1
name: hello-check
description: Say hi through the stub of your choice
risk: dangerous
kind: pipeline
steps:
  - bash: echo hello
`

func setupRfxDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CRV_RFX_DIR", dir)
	return dir
}

func TestRfxInstallListShowRemove(t *testing.T) {
	dir := setupRfxDir(t)
	src := filepath.Join(t.TempDir(), "hello-check.rfx.yaml")
	if err := os.WriteFile(src, []byte(testReflex), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (&client{}).cmdRfx([]string{"install", src}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello-check.rfx.yaml")); err != nil {
		t.Fatal("file not copied into rfx dir")
	}
	// Second install must refuse (no silent overwrites).
	if err := (&client{}).cmdRfx([]string{"install", src}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite not refused: %v", err)
	}
	if err := (&client{}).cmdRfx([]string{"list"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := (&client{}).cmdRfx([]string{"show", "hello-check"}); err != nil {
		t.Fatalf("show: %v", err)
	}
	if err := (&client{}).cmdRfx([]string{"test", "hello-check"}); err != nil {
		t.Fatalf("test: %v", err)
	}
	if err := (&client{}).cmdRfx([]string{"remove", "hello-check"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := (&client{}).cmdRfx([]string{"show", "hello-check"}); err == nil {
		t.Fatal("show succeeded after remove")
	}
}

func TestRfxInstallRejectsInvalid(t *testing.T) {
	dir := setupRfxDir(t)
	src := filepath.Join(t.TempDir(), "bad-one.rfx.yaml")
	// Stem says bad-one, content says other-name → must refuse.
	bad := strings.Replace(testReflex, "name: hello-check", "name: other-name", 1)
	if err := os.WriteFile(src, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (&client{}).cmdRfx([]string{"install", src}); err == nil || !strings.Contains(err.Error(), "NOT installed") {
		t.Fatalf("invalid manifest installed: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("invalid file landed in the rfx dir anyway")
	}
}

func TestRfxInstallRejectsWrongSuffix(t *testing.T) {
	setupRfxDir(t)
	src := filepath.Join(t.TempDir(), "hello.yaml")
	if err := os.WriteFile(src, []byte(testReflex), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (&client{}).cmdRfx([]string{"install", src}); err == nil || !strings.Contains(err.Error(), ".rfx.yaml") {
		t.Fatalf("wrong suffix accepted: %v", err)
	}
}

func TestExtractManifest(t *testing.T) {
	fenced := "Here is your manifest:\n```yaml\nrfx: 1\nname: x\ndescription: d\n```\nHope that helps!"
	got, err := extractManifest(fenced)
	if err != nil || !strings.Contains(got, "rfx: 1") || strings.Contains(got, "Hope") {
		t.Fatalf("fenced extraction: %q, %v", got, err)
	}
	bare := "Sure!\nrfx: 1\nname: y\ndescription: d"
	if got, err := extractManifest(bare); err != nil || !strings.HasPrefix(got, "rfx: 1") {
		t.Fatalf("bare extraction: %q, %v", got, err)
	}
	if _, err := extractManifest("no manifest here"); err == nil {
		t.Fatal("missing manifest not rejected")
	}
}
