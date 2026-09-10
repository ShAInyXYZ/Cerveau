package rfxdgv

import (
	"os"
	"path/filepath"
	"testing"

	"cerveau/internal/rfx"
)

func TestDGVPackManifests(t *testing.T) {
	dir := filepath.Join("..", "..", "rfx", "dgv")
	manifest, err := os.ReadFile(filepath.Join(dir, "pack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := rfx.ParsePack(manifest, filepath.Join(dir, "pack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rfx.ValidatePack(p); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.rfx.yaml"))
	if err != nil || len(files) != 7 {
		t.Fatalf("expected seven narrow talents: %v %d", err, len(files))
	}
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
			t.Fatalf("%s: %v", file, err)
		}
		if len(def.Argv) != 3 || def.Argv[0] != "/usr/bin/env" || def.Argv[1] != "rfx-dgv" {
			t.Fatalf("nonportable/helper-bypass argv: %v", def.Argv)
		}
	}
}
