package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"sync"
)

const (
	MaxImages         = 2
	MaxImageBytes     = 256 * 1024
	MaxImageDimension = 1280
	MaxImagePixels    = MaxImageDimension * MaxImageDimension
	// A conservative reservation, not a claim about this Core's vision tokenizer.
	// detail:low requests cheap vision but may be ignored by a compatible server.
	VisionTokensPerImage = 4096
)

// Image is accepted by data_url only. Metadata is always derived from decoded
// bytes before admission and replay, never trusted from the caller or journal.
type Image struct {
	DataURL string `json:"data_url"`
	SHA256  string `json:"sha256,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	MIME    string `json:"mime,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
}

func ValidateImages(inputs []Image) ([]Image, error) {
	return ValidateImagesContext(context.Background(), inputs)
}

func ValidateImagesContext(ctx context.Context, inputs []Image) ([]Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(inputs) > MaxImages {
		return nil, fmt.Errorf("images: maximum %d attachments per message", MaxImages)
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([]Image, 0, len(inputs))
	for i, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		prefix := ""
		for _, p := range []string{"data:image/png;base64,", "data:image/jpeg;base64,"} {
			if strings.HasPrefix(input.DataURL, p) {
				prefix = p
				break
			}
		}
		if prefix == "" {
			return nil, fmt.Errorf("images[%d]: only inline PNG or JPEG data URLs are accepted", i)
		}
		encoded := strings.TrimPrefix(input.DataURL, prefix)
		if len(encoded) > base64.StdEncoding.EncodedLen(MaxImageBytes) || strings.ContainsAny(encoded, "\r\n\t ") {
			return nil, fmt.Errorf("images[%d]: invalid base64 or image exceeds 256 KiB", i)
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("images[%d]: invalid base64", i)
		}
		im, err := ImageFromBytesContext(ctx, raw)
		if err != nil {
			return nil, fmt.Errorf("images[%d]: %w", i, err)
		}
		if prefix != "data:"+im.MIME+";base64," {
			return nil, fmt.Errorf("images[%d]: declared MIME does not match decoded image", i)
		}
		out = append(out, im)
	}
	return out, nil
}

func ImageFromBytes(raw []byte) (Image, error) {
	return ImageFromBytesContext(context.Background(), raw)
}

// The cache retains only derived metadata keyed by the exact bytes' digest.
// It never retains an image's base64, raster, session, or caller metadata.
type imageMetadata struct {
	Width, Height int
	MIME          string
	Bytes         int
}
type imageValidationCache struct {
	mu      sync.Mutex
	limit   int
	entries map[[32]byte]imageMetadata
	order   [][32]byte
}

func newImageValidationCache(limit int) *imageValidationCache {
	return &imageValidationCache{limit: limit, entries: map[[32]byte]imageMetadata{}}
}
func (c *imageValidationCache) get(key [32]byte) (imageMetadata, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.entries[key]
	return value, ok
}
func (c *imageValidationCache) put(key [32]byte, value imageMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; exists {
		return
	}
	if len(c.order) >= c.limit {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
	c.entries[key] = value
	c.order = append(c.order, key)
}

var validatedImages = newImageValidationCache(128)

func imageWithMetadata(raw []byte, key [32]byte, metadata imageMetadata) Image {
	return Image{DataURL: "data:" + metadata.MIME + ";base64," + base64.StdEncoding.EncodeToString(raw), SHA256: fmt.Sprintf("%x", key), Width: metadata.Width, Height: metadata.Height, MIME: metadata.MIME, Bytes: metadata.Bytes}
}

func ImageFromBytesContext(ctx context.Context, raw []byte) (Image, error) {
	if err := ctx.Err(); err != nil {
		return Image{}, err
	}
	if len(raw) == 0 || len(raw) > MaxImageBytes {
		return Image{}, fmt.Errorf("image must contain 1 to 262144 bytes")
	}
	key := sha256.Sum256(raw)
	if metadata, ok := validatedImages.get(key); ok {
		return imageWithMetadata(raw, key, metadata), nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") {
		return Image{}, fmt.Errorf("image is not a valid PNG or JPEG")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxImageDimension || config.Height > MaxImageDimension || config.Width*config.Height > MaxImagePixels {
		return Image{}, fmt.Errorf("image dimensions must be at most 1280 by 1280")
	}
	// Decode the complete bounded raster too: a plausible header is not evidence
	// that a truncated/corrupt image can be consumed by the model.
	if _, _, err = image.Decode(bytes.NewReader(raw)); err != nil {
		return Image{}, fmt.Errorf("image raster is corrupt or incomplete")
	}
	if err := ctx.Err(); err != nil {
		return Image{}, err
	}
	metadata := imageMetadata{Width: config.Width, Height: config.Height, MIME: "image/" + format, Bytes: len(raw)}
	validatedImages.put(key, metadata)
	return imageWithMetadata(raw, key, metadata), nil
}

type imageURLPart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}
type contentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *imageURLPart `json:"image_url,omitempty"`
}

// MarshalJSON preserves the old text-only wire shape exactly. Binary data is
// represented as image_url parts, never prompt text, tool arguments or metadata.
func (m Message) MarshalJSON() ([]byte, error) {
	type plain Message
	if len(m.Images) == 0 {
		return json.Marshal(plain(m))
	}
	images, err := ValidateImages(m.Images)
	if err != nil {
		return nil, err
	}
	if m.Role != "user" {
		return nil, fmt.Errorf("image attachments are supported only on user messages")
	}
	parts := make([]contentPart, 0, len(images)+1)
	if m.Content != "" {
		parts = append(parts, contentPart{Type: "text", Text: m.Content})
	}
	for _, im := range images {
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURLPart{URL: im.DataURL, Detail: "low"}})
	}
	return json.Marshal(struct {
		*plain
		Content []contentPart `json:"content"`
	}{plain: (*plain)(&m), Content: parts})
}

func (m *Message) UnmarshalJSON(raw []byte) error {
	type plain Message
	var p plain
	wire := struct {
		*plain
		Content json.RawMessage `json:"content"`
	}{plain: &p}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	if len(wire.Content) > 0 && string(wire.Content) != "null" {
		if err := json.Unmarshal(wire.Content, &p.Content); err != nil {
			var parts []contentPart
			if err = json.Unmarshal(wire.Content, &parts); err != nil {
				return fmt.Errorf("message content must be text or supported content parts: %w", err)
			}
			texts := []string{}
			for _, part := range parts {
				switch part.Type {
				case "text":
					texts = append(texts, part.Text)
				case "image_url":
					if part.ImageURL == nil {
						return fmt.Errorf("image_url part requires a URL")
					}
					p.Images = append(p.Images, Image{DataURL: part.ImageURL.URL})
				default:
					return fmt.Errorf("unsupported message content part %q", part.Type)
				}
			}
			p.Content = strings.Join(texts, "\n")
			p.Images, err = ValidateImages(p.Images)
			if err != nil {
				return err
			}
		}
	}
	*m = Message(p)
	return nil
}
