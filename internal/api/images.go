package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"cerveau/internal/llm"
	"cerveau/internal/loop"
)

// Two maximum-size base64 images plus bounded command text/metadata fit in this
// envelope. MaxBytesReader also covers chunked requests without Content-Length.
const maxCommandBody = 768 * 1024

func decodeBoundedCommand(w http.ResponseWriter, r *http.Request, dest any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxCommandBody)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	err := d.Decode(dest)
	if err == nil {
		var extra any
		if err = d.Decode(&extra); err == io.EOF {
			return true
		}
	}
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "command body exceeds 768 KiB"})
	} else {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected one valid command JSON object with supported fields"})
	}
	return false
}

var imageHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func normalizeUserEventImages(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil || fields == nil {
		return nil, fmt.Errorf("msg.user payload must be an object")
	}
	var message loop.UserMessage
	if json.Unmarshal(payload, &message) != nil {
		return nil, fmt.Errorf("msg.user text and images have invalid types")
	}
	images, err := llm.ValidateImagesContext(ctx, message.Images)
	if err != nil {
		return nil, err
	}
	if len(images) > 0 {
		fields["images"], _ = json.Marshal(images)
		if strings.TrimSpace(message.Text) == "" {
			fields["text"], _ = json.Marshal(loop.ImageOnlyText)
		}
	} else {
		delete(fields, "images")
	}
	return json.Marshal(fields)
}

// DevCheckImage resolves only a session's explicit .devcheck artifact, checks
// the caller's expected receipt digest, and returns a validated inline image.
// It is not a general-purpose file read endpoint and never attaches by itself.
func (a *API) DevCheckImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !requireJSON(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "artifact path and sha256 required"})
		return
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "expected one artifact request"})
		return
	}
	if !strings.HasPrefix(body.Path, ".devcheck/") || filepath.Clean(body.Path) != body.Path || filepath.IsAbs(body.Path) || strings.ContainsAny(body.Path, "\x00\\") || !imageHashPattern.MatchString(body.SHA256) {
		writeJSON(w, 400, map[string]string{"error": "canonical .devcheck-relative path and lowercase SHA-256 required"})
		return
	}
	meta, err := a.sess.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "session not found"})
		return
	}
	raw, err := readDevCheckImage(meta.Workspace, body.Path)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "artifact must be a bounded regular file beneath this session workspace, without symlinks"})
		return
	}
	image, err := llm.ImageFromBytesContext(r.Context(), raw)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if image.SHA256 != body.SHA256 {
		writeJSON(w, 409, map[string]string{"error": "artifact changed; inspect the new capture receipt before attaching"})
		return
	}
	writeJSON(w, 200, map[string]any{"image": image, "path": body.Path, "session_id": meta.ID})
}
