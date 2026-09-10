package loop

import (
	"context"
	"encoding/json"
	"fmt"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/window"
)

const ImageOnlyText = "[Image attachment]"

type UserMessage struct {
	Text   string      `json:"text"`
	Images []llm.Image `json:"images,omitempty"`
}

type imageContextKey struct{}

func imagesOf(ctx context.Context) []llm.Image {
	images, _ := ctx.Value(imageContextKey{}).([]llm.Image)
	return append([]llm.Image(nil), images...)
}

func unavailableImageText(text, reason string) string {
	return text + "\n[Image attachment unavailable: " + reason + ". Do not claim to have seen this image; ask for a new attachment if it is needed.]"
}

func replayUserMessage(raw json.RawMessage) (llm.Message, bool) {
	// Decode the optional attachment field separately so one malformed imported
	// image cannot make the associated user text vanish from the conversation.
	var p struct {
		Text   string          `json:"text"`
		Images json.RawMessage `json:"images"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return llm.Message{}, false
	}
	m := llm.Message{Role: "user", Content: p.Text}
	if len(p.Images) > 0 && json.Unmarshal(p.Images, &m.Images) != nil {
		m.Images = nil
		m.Content = unavailableImageText(m.Content, "invalid stored attachment shape")
	}
	return m, true
}

// Full image validation happens after the window has selected retained history.
// Compacted pictures therefore never consume raster-decoding CPU on each tool
// iteration. Invalid retained imports remain visible as unavailable context;
// they do not poison every subsequent, unrelated user turn in the session.
func validateRetainedImages(ctx context.Context, messages []llm.Message, report window.Report) ([]llm.Message, window.Report, error) {
	for i := range messages {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
		if len(messages[i].Images) == 0 {
			continue
		}
		images, err := llm.ValidateImagesContext(ctx, messages[i].Images)
		if ctx.Err() != nil {
			return nil, report, ctx.Err()
		}
		if err != nil {
			messages[i].Images = nil
			messages[i].Content = unavailableImageText(messages[i].Content, err.Error())
		} else {
			messages[i].Images = images
		}
	}
	// Tokens remains a conservative estimate when a bad image is replaced by a
	// short note; final Admit independently counts the actual outgoing envelope.
	report.VisionTokens = 0
	for _, message := range messages {
		report.VisionTokens += len(message.Images) * llm.VisionTokensPerImage
	}
	return messages, report, nil
}

// taskImages mirrors taskBrief: only the user request immediately before the
// latest committed plan belongs to this plan, never an older task's pictures.
func taskImages(path string) ([]llm.Image, error) {
	events, err := episodic.Replay(path)
	if err != nil {
		return nil, err
	}
	end := len(events)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == episodic.Plan {
			end = i
			break
		}
	}
	for i := end - 1; i >= 0; i-- {
		if events[i].Type == episodic.MsgUser {
			var p UserMessage
			if err = json.Unmarshal(events[i].Payload, &p); err != nil {
				return nil, fmt.Errorf("decode plan image source %s: %w", events[i].ID, err)
			}
			return llm.ValidateImages(p.Images)
		}
	}
	return nil, nil
}
