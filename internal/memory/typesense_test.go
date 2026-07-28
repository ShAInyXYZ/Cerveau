package memory

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/config"
)

func TestAssetName(t *testing.T) {
	n, err := assetName("linux", "amd64")
	if err != nil || !strings.Contains(n, "linux-amd64") {
		t.Fatalf("assetName = %q, %v", n, err)
	}
	if _, err := assetName("plan9", "mips"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}

func makeTgz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestInstallExtractsBinary(t *testing.T) {
	payload := []byte("#!/bin/sh\necho stub\n")
	tgz := makeTgz(t, "typesense-server", payload)
	sum := sha256.Sum256(tgz)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			fmt.Fprint(w, hex.EncodeToString(sum[:]))
			return
		}
		w.Write(tgz)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "bin", "typesense-server")
	if err := InstallFrom(dest, srv.URL, "27.1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("extracted content mismatch")
	}
	info, _ := os.Stat(dest)
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("binary not executable")
	}
}

func TestInstallChecksumMismatch(t *testing.T) {
	tgz := makeTgz(t, "typesense-server", []byte("x"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			fmt.Fprint(w, strings.Repeat("0", 64))
			return
		}
		w.Write(tgz)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "typesense-server")
	if err := InstallFrom(dest, srv.URL, "27.1"); err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

func TestEnsureAdoptsCustomEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Endpoints.Typesense = srv.URL
	ep, err := EnsureTypesense(cfg, filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ep.URL != srv.URL || ep.Managed {
		t.Fatalf("ep = %+v", ep)
	}
}

func TestEnsureFailsOnUnreachableCustom(t *testing.T) {
	cfg := config.Default()
	cfg.Endpoints.Typesense = "http://localhost:1"
	_, err := EnsureTypesense(cfg, filepath.Join(t.TempDir(), "config.json"))
	if err == nil {
		t.Fatal("expected unreachable error")
	}
}

func TestGenKey(t *testing.T) {
	k, err := genKey()
	if err != nil || len(k) != 48 {
		t.Fatalf("key = %q (%d chars)", k, len(k))
	}
}
