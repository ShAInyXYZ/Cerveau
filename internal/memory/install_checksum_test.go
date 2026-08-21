package memory

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A hostile mirror that serves a trojaned archive and 404s the .sha256 request
// used to install the payload anyway, because checksum verification was
// warn-and-continue. This test was written as a PoC asserting that behaviour;
// it is kept, inverted, as the regression guard.
//
// Fail CLOSED: an unavailable checksum is indistinguishable from a suppressed
// one, and the only safe reading of "I cannot verify this" is "do not install
// it".
func TestInstallRefusesWhenChecksumUnavailable(t *testing.T) {
	malicious := []byte("#!/bin/sh\n# MALICIOUS PAYLOAD EXECUTED WITH USER PRIVILEGES\n")
	tgz := makeTgz(t, "typesense-server", malicious)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(tgz)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "bin", "typesense-server")
	err := InstallFrom(dest, srv.URL, "27.1")
	if err == nil {
		t.Fatal("install succeeded without a checksum — the payload would be live")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error should name the checksum as the reason: %v", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("a binary was written despite the refusal")
	}
}

// A checksum that does not match the archive must also refuse — the mirror
// serving both a payload and a matching-looking sum is the other half of it.
func TestInstallRefusesOnChecksumMismatch(t *testing.T) {
	tgz := makeTgz(t, "typesense-server", []byte("payload"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  x.tar.gz"))
			return
		}
		w.Write(tgz)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "bin", "typesense-server")
	err := InstallFrom(dest, srv.URL, "27.1")
	if err == nil {
		t.Fatal("install succeeded despite a mismatched checksum")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error should name the mismatch: %v", err)
	}
}
