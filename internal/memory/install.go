package memory

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const typesenseVersion = "27.1"

var typesenseBaseURL = "https://dl.typesense.org/releases"

func assetName(goos, goarch string) (string, error) {
	platform := ""
	switch goos + "/" + goarch {
	case "linux/amd64":
		platform = "linux-amd64"
	case "linux/arm64":
		platform = "linux-arm64"
	case "darwin/amd64":
		platform = "darwin-amd64"
	case "darwin/arm64":
		platform = "darwin-arm64"
	default:
		return "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
	}
	return fmt.Sprintf("typesense-server-%s-%s.tar.gz", typesenseVersion, platform), nil
}

func Install(destPath string) error {
	return InstallFrom(destPath, typesenseBaseURL, typesenseVersion)
}

func InstallFrom(destPath, baseURL, version string) error {
	asset, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/%s/%s", strings.TrimRight(baseURL, "/"), version, asset)
	sums, sumErr := fetchText(url + ".sha256")
	if sumErr == nil && sums != "" {
		slog.Info("typesense: verifying checksum")
	}
	data, err := fetchBytes(url)
	if err != nil {
		return err
	}
	if sums != "" {
		want := strings.ToLower(strings.Fields(sums)[0])
		got := strings.ToLower(hex.EncodeToString(sha256Of(data)))
		if want != got {
			return fmt.Errorf("checksum mismatch: want %s got %s", want, got)
		}
	} else {
		slog.Warn("typesense: no checksum available, proceeding unverified")
	}
	return extractBinary(data, destPath)
}

var downloadClient = &http.Client{Timeout: 10 * time.Minute}

func fetchBytes(url string) ([]byte, error) {
	resp, err := downloadClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func fetchText(url string) (string, error) {
	data, err := fetchBytes(url)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func sha256Of(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func extractBinary(tgz []byte, destPath string) error {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if filepath.Base(hdr.Name) != "typesense-server" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	}
	return fmt.Errorf("typesense-server not found in archive")
}

func genKey() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
