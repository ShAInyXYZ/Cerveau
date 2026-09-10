package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func testImageURL(t *testing.T, format string, width, height int) string {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, width, height))
	im.Set(0, 0, color.RGBA{R: 120, A: 255})
	var b bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&b, im, nil)
	} else {
		err = png.Encode(&b, im)
	}
	if err != nil {
		t.Fatal(err)
	}
	return "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestImageValidationCacheIsBoundedAndContainsMetadataOnly(t *testing.T) {
	cache := newImageValidationCache(2)
	for i := byte(0); i < 3; i++ {
		key := sha256.Sum256([]byte{i})
		cache.put(key, imageMetadata{Width: int(i) + 1, Height: 1, MIME: "image/png", Bytes: 20})
	}
	if len(cache.entries) != 2 {
		t.Fatalf("unbounded cache size %d", len(cache.entries))
	}
	if _, ok := cache.get(sha256.Sum256([]byte{0})); ok {
		t.Fatal("oldest validation was not evicted")
	}
	if got, ok := cache.get(sha256.Sum256([]byte{2})); !ok || got.Width != 3 {
		t.Fatalf("cache metadata %+v %v", got, ok)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ValidateImagesContext(ctx, []Image{{DataURL: testImageURL(t, "png", 4, 4)}}); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestImageValidationDerivesMetadataAndRejectsUnsafeInputs(t *testing.T) {
	url := testImageURL(t, "png", 32, 24)
	images, err := ValidateImages([]Image{{DataURL: url, Width: 99999, Height: 99999, SHA256: "forged", MIME: "image/svg+xml", Bytes: 99999}})
	if err != nil || len(images) != 1 || images[0].Width != 32 || images[0].Height != 24 || len(images[0].SHA256) != 64 || images[0].MIME != "image/png" || images[0].Bytes == 99999 {
		t.Fatalf("derived %+v %v", images, err)
	}
	for _, bad := range []string{"https://example.invalid/image.png", "data:image/svg+xml;base64,PHN2Zy8+", "data:image/png;base64,broken", strings.Replace(url, "image/png", "image/jpeg", 1), testImageURL(t, "png", 1281, 1), "data:image/png;base64," + strings.Repeat("A", MaxImageBytes*2)} {
		if _, err := ValidateImages([]Image{{DataURL: bad}}); err == nil {
			t.Fatalf("accepted %.80s", bad)
		}
	}
	if _, err := ValidateImages([]Image{{DataURL: url}, {DataURL: url}, {DataURL: url}}); err == nil {
		t.Fatal("accepted three images")
	}
	if _, err := ValidateImages([]Image{{DataURL: testImageURL(t, "jpeg", 20, 10)}}); err != nil {
		t.Fatal(err)
	}
}

func TestMessageTextCompatibilityAndMultimodalRoundTrip(t *testing.T) {
	plain, err := json.Marshal(Message{Role: "user", Content: "hello"})
	if err != nil || string(plain) != `{"role":"user","content":"hello"}` {
		t.Fatalf("text changed: %s %v", plain, err)
	}
	images, err := ValidateImages([]Image{{DataURL: testImageURL(t, "png", 8, 8)}})
	if err != nil {
		t.Fatal(err)
	}
	m := Message{Role: "user", Content: "check this", Images: images}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL struct {
				URL    string `json:"url"`
				Detail string `json:"detail"`
			} `json:"image_url"`
		} `json:"content"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil || len(wire.Content) != 2 || wire.Content[1].ImageURL.URL != images[0].DataURL || wire.Content[1].ImageURL.Detail != "low" {
		t.Fatalf("wire: %s %v", raw, err)
	}
	var back Message
	if err = json.Unmarshal(raw, &back); err != nil || back.Content != m.Content || len(back.Images) != 1 || back.Images[0].SHA256 != images[0].SHA256 {
		t.Fatalf("roundtrip: %+v %v", back, err)
	}
}
