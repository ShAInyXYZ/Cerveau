package window

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	"cerveau/internal/llm"
)

func TestVisionReservationDoesNotTokenizeBase64(t *testing.T) {
	counter := CounterFunc(func(_ context.Context, text string) int {
		if strings.Contains(text, "base64") {
			t.Fatal("binary was passed to text tokenizer")
		}
		return len(text) / 4
	})
	m := NewManager(32768, 2048, counter)
	message := llm.Message{Role: "user", Content: "inspect", Images: []llm.Image{{DataURL: "data:image/png;base64,not-counted-here"}}}
	_, report := m.Build(context.Background(), []Item{{Kind: "user", Msg: message}})
	if report.Tokens < llm.VisionTokensPerImage || report.VisionTokens != llm.VisionTokensPerImage {
		t.Fatalf("missing vision reservation %+v", report)
	}
	// Admission separately validates actual image bytes; bogus data cannot pass.
	if err := m.Admit(context.Background(), []llm.Message{message}, nil, 2048); err == nil {
		t.Fatal("invalid image admitted")
	}
}

func TestAdmissionReservesVisionCapacitySeparatelyFromText(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	images, err := llm.ValidateImages([]llm.Image{{DataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())}})
	if err != nil {
		t.Fatal(err)
	}
	counter := CounterFunc(func(_ context.Context, text string) int {
		if strings.Contains(text, "base64") {
			t.Fatal("binary reached text tokenizer")
		}
		return len(text) / 4
	})
	m := NewManager(5000, 2048, counter)
	plain := llm.Message{Role: "user", Content: "check the screenshot"}
	if err = m.Admit(context.Background(), []llm.Message{plain}, nil, 2048); err != nil {
		t.Fatal(err)
	}
	visual := plain
	visual.Images = images
	if err = m.Admit(context.Background(), []llm.Message{visual}, nil, 2048); err == nil {
		t.Fatal("image reservation ignored under tight budget")
	}
	large := NewManager(32768, 2048, counter)
	if err = large.Admit(context.Background(), []llm.Message{visual}, nil, 2048); err != nil {
		t.Fatal(err)
	}
}
