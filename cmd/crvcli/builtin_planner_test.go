package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRfxListIncludesBuiltinPlannerWithoutInstallingFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("CRV_RFX_DIR", dir)
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = previous; read.Close(); write.Close() }()
	err = rfxList()
	write.Close()
	output, readErr := io.ReadAll(read)
	if err != nil || readErr != nil {
		t.Fatalf("list: %v; output: %v", err, readErr)
	}
	if !strings.Contains(string(output), "planner") || !strings.Contains(string(output), "builtin") || !strings.Contains(string(output), "supervisor") {
		t.Fatalf("built-in not visible: %s", output)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("list created installed directory: %v", err)
	}
}
