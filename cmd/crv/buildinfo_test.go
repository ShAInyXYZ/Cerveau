package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"cerveau/internal/api"
)

func TestBuildInfoFlagDoesNotLoadConfigOrStartServices(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "not-created", "config.json")
	previousArgs, previousFlags, previousOut := os.Args, flag.CommandLine, os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.Args, flag.CommandLine, os.Stdout = previousArgs, previousFlags, previousOut
		read.Close()
		write.Close()
	}()
	os.Args = []string{"crv", "-config", configPath, "-build-info"}
	flag.CommandLine = flag.NewFlagSet("crv", flag.ContinueOnError)
	os.Stdout = write
	main()
	write.Close()
	data, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	var info api.BuildInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("invalid JSON: %s: %v", data, err)
	}
	if info.Version != api.Version || info.Revision != api.BuildRevision || info.Planner.Loaded || info.Planner.Origin != "builtin" || info.Planner.ContentSHA256 == "" {
		t.Fatalf("wrong binary identity: %s", data)
	}
	if _, err := os.Stat(filepath.Dir(configPath)); !os.IsNotExist(err) {
		t.Fatalf("build-info touched config directory: %v", err)
	}
}
