package devcheck

import (
	"os"
	"path/filepath"
	"testing"

	"cerveau/internal/rfx"
)

func TestDevCheckManifestsAreRunnableNarrowExecTalents(t *testing.T) {
	packData, err := os.ReadFile("pack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := rfx.ParsePack(packData, "pack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := rfx.ValidatePack(pack); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"inspect", "check", "capture"} {
		name := "devcheck-" + action
		data, err := os.ReadFile(name + ".rfx.yaml")
		if err != nil {
			t.Fatal(err)
		}
		reflex, err := rfx.Parse(data, name+".rfx.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if err := rfx.Validate(reflex, nil); err != nil {
			t.Fatal(err)
		}
		if reflex.Name != name || reflex.Kind != rfx.KindExec || reflex.Risk != rfx.RiskSensitive {
			t.Fatalf("unexpected contract: %+v", reflex)
		}
		if len(reflex.Argv) != 3 || reflex.Argv[0] != "/usr/bin/env" || reflex.Argv[1] != "rfx-devcheck" || reflex.Argv[2] != action {
			t.Fatalf("not the packaged PATH runner: %v", reflex.Argv)
		}
		if !reflex.Card.Subprocess || len(reflex.Card.FS) != 1 || reflex.Card.FS[0] != "workspace" {
			t.Fatalf("bad capability scope: %+v", reflex.Card)
		}
		if _, err := os.Stat(filepath.Join("..", "..", "scripts", "devcheck", "rfx-devcheck.mjs")); err != nil {
			t.Fatal(err)
		}
	}
}
