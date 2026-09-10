package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cerveau/internal/config"
	"cerveau/internal/llm"
	"cerveau/internal/session"
)

func apiImageBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 12, 8))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDevCheckImageReadIsSessionScopedHashedAndNoSymlinks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fail-closed no-symlink artifact reader is Linux-only")
	}
	root := t.TempDir()
	ws := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(ws, ".devcheck", "capture"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewFSStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.CreateInWorkspace("images", ws)
	if err != nil {
		t.Fatal(err)
	}
	a := New(&config.Config{Workspace: t.TempDir()}, store)
	raw := apiImageBytes(t)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	path := ".devcheck/capture/image.png"
	if err = os.WriteFile(filepath.Join(ws, path), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "outside.png"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(root, "outside.png"), filepath.Join(ws, ".devcheck", "link.png")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(root, filepath.Join(ws, ".devcheck", "link-dir")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, hash string
		status     int
	}{
		{path, hash, 200}, {path, strings.Repeat("0", 64), 409}, {"../outside.png", hash, 400}, {".devcheck/../outside.png", hash, 400}, {"outside.png", hash, 400}, {".devcheck/link.png", hash, 400}, {".devcheck/link-dir/outside.png", hash, 400}, {".devcheck/capture", hash, 400},
	} {
		body, _ := json.Marshal(map[string]string{"path": tc.path, "sha256": tc.hash})
		request := httptest.NewRequest("POST", "/api/sessions/"+meta.ID+"/images/devcheck", bytes.NewReader(body))
		request.SetPathValue("id", meta.ID)
		request.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.DevCheckImage(w, request)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var got struct {
				Image llm.Image `json:"image"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Image.SHA256 != hash || got.Image.Width != 12 || got.Image.Height != 8 {
				t.Fatalf("image: %+v %v", got, err)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("image response can be cached")
			}
		}
	}
}

func TestCommandImageBoundsAndMalformedBodies(t *testing.T) {
	f := newRunAPIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "done"}}}})
	})
	for _, raw := range []string{strings.Repeat(" ", maxCommandBody+1), `{"command_id":"bad","kind":"chat","text":"x","images":[{"data_url":"https://example.invalid/image"}]}`, `{"command_id":"bad","kind":"chat","text":"x"} {}`} {
		request := httptest.NewRequest("POST", "/commands", strings.NewReader(raw))
		request.SetPathValue("id", f.sid)
		w := httptest.NewRecorder()
		f.a.Command(w, request)
		if w.Code != 400 && w.Code != 413 {
			t.Fatalf("invalid body admitted: %d %s", w.Code, w.Body.String())
		}
	}
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(apiImageBytes(t))
	status, result := f.post(t, "commands", map[string]any{"command_id": "image-only", "kind": "chat", "mode": "discussion", "images": []any{map[string]string{"data_url": url}}})
	if status != 202 {
		t.Fatalf("image-only command %d %v", status, result)
	}
}

func TestAppendUserEventEnforcesImageContractBeforeJournal(t *testing.T) {
	f := newRunAPIFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected model call") })
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(apiImageBytes(t))
	for _, tc := range []struct {
		body, contentType string
		want              int
	}{
		{`{"type":"msg.user","payload":{"text":"x","images":[{"data_url":"https://example.invalid/x"}]}}`, "application/json", 400},
		{`{"type":"msg.user","payload":{"text":"x","images":"broken"}}`, "application/json", 400},
		{strings.Repeat(" ", maxCommandBody+1), "application/json", 413},
		{`{"type":"msg.user","payload":{"text":"x"}}`, "text/plain", 415},
		{`{"type":"msg.user","payload":{"images":[{"data_url":"` + url + `","sha256":"forged","width":99999}],"steer":true}}`, "application/json", 201},
	} {
		request := httptest.NewRequest("POST", "/events", strings.NewReader(tc.body))
		request.SetPathValue("id", f.sid)
		request.Header.Set("Content-Type", tc.contentType)
		w := httptest.NewRecorder()
		f.a.AppendEvent(w, request)
		if w.Code != tc.want {
			t.Fatalf("status %d want %d: %s", w.Code, tc.want, w.Body.String())
		}
		if w.Code == 201 {
			var got struct {
				Payload struct {
					Text   string      `json:"text"`
					Images []llm.Image `json:"images"`
					Steer  bool        `json:"steer"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.Payload.Images) != 1 || got.Payload.Images[0].Width != 12 || len(got.Payload.Images[0].SHA256) != 64 || !got.Payload.Steer || got.Payload.Text != "[Image attachment]" {
				t.Fatalf("normalized payload: %+v %v", got, err)
			}
		}
	}
}
