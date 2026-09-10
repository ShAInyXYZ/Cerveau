package loop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"

	"cerveau/internal/episodic"
	"cerveau/internal/llm"
	"cerveau/internal/window"
)

func loopImage(t *testing.T, red uint8) llm.Image {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 4, 4))
	im.Set(0, 0, color.RGBA{R: red, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return llm.Image{DataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())}
}

func TestInvalidHistoricalImageIsUnavailableNotSessionPoison(t *testing.T) {
	l, _ := setupInterruptLoop(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected model call") })
	writer, err := l.open("s1")
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []any{UserMessage{Text: "old attachment", Images: []llm.Image{{DataURL: "https://example.invalid/old.png"}}}, map[string]any{"text": "malformed image shape", "images": "not an array"}, UserMessage{Text: "new unrelated question"}} {
		if _, err = writer.Append(episodic.MsgUser, payload); err != nil {
			t.Fatal(err)
		}
	}
	msgs, _, err := l.buildMessages(context.Background(), "s1", "sys", nil, nil)
	if err != nil {
		t.Fatalf("history poisoned session: %v", err)
	}
	unavailable := 0
	for _, msg := range msgs {
		if strings.Contains(msg.Content, "Image attachment unavailable") {
			unavailable++
			if len(msg.Images) > 0 || !strings.Contains(msg.Content, "Do not claim to have seen") {
				t.Fatalf("dishonest image fallback %+v", msg)
			}
		}
	}
	if unavailable != 2 {
		t.Fatalf("unavailable image notes=%d", unavailable)
	}
}

func TestImageReplayCompactsBeforeRasterValidationAndObservesCancel(t *testing.T) {
	l, _ := setupInterruptLoop(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected model call") })
	writer, err := l.open("s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Append(episodic.MsgUser, UserMessage{Text: "OLD_BAD_IMAGE", Images: []llm.Image{{DataURL: "invalid old raster"}}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err = writer.Append(episodic.MsgUser, UserMessage{Text: "new text"}); err != nil {
			t.Fatal(err)
		}
	}
	l.win = window.NewManager(4000, 1024, window.CounterFunc(func(context.Context, string) int { return 4 }))
	msgs, report, err := l.buildMessages(context.Background(), "s1", "sys", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Trimmed == 0 {
		t.Fatal("old image was validated/stripped before its vision reservation could trigger compaction")
	}
	for _, msg := range msgs {
		if len(msg.Images) > 0 || strings.Contains(msg.Content, "Image attachment unavailable") {
			t.Fatalf("dropped image was processed: %+v", msg)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = l.buildMessages(ctx, "s1", "sys", nil, nil); err != context.Canceled {
		t.Fatalf("replay ignored cancellation: %v", err)
	}
}

func TestImageCommandAdmissionReplayAndIdempotency(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var captured []llm.Message
	l, path := setupInterruptLoop(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		captured = body.Messages
		close(entered)
		<-release
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{textReply("seen")}})
	})
	cmd := Command{ID: "image-once", Kind: "chat", Mode: "discussion", Images: []llm.Image{loopImage(t, 10)}}
	state, err := l.Start("s1", cmd, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if len(captured) == 0 || len(captured[len(captured)-1].Images) != 1 {
		t.Fatalf("model lost image: %+v", captured)
	}
	duplicate := cmd
	duplicate.Images = append([]llm.Image(nil), cmd.Images...)
	duplicate.Images[0].SHA256 = "forged metadata"
	if got, err := l.Start("s1", duplicate, nil); err != nil || got.ID != state.ID {
		t.Fatalf("canonical dedup: %+v %v", got, err)
	}
	changed := cmd
	changed.Images = []llm.Image{loopImage(t, 30)}
	if _, err = l.Start("s1", changed, nil); err == nil || !strings.Contains(err.Error(), "different request") {
		t.Fatalf("image idempotency not bound: %v", err)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for len(l.RunningSessions()) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	events, err := episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	users := 0
	for _, ev := range events {
		if ev.Type == episodic.MsgUser {
			users++
			var payload UserMessage
			if err = json.Unmarshal(ev.Payload, &payload); err != nil || len(payload.Images) != 1 || len(payload.Images[0].SHA256) != 64 || payload.Text != ImageOnlyText {
				t.Fatalf("journal image: %s %v", ev.Payload, err)
			}
		}
	}
	if users != 1 {
		t.Fatalf("user events=%d", users)
	}
	messages, _, err := l.buildMessages(context.Background(), "s1", "sys", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	images := 0
	for _, msg := range messages {
		images += len(msg.Images)
	}
	if images != 1 {
		t.Fatalf("replay images=%d", images)
	}
}

func TestImageValidationBeforeRunAdmission(t *testing.T) {
	l, path := setupInterruptLoop(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected model call") })
	for _, cmd := range []Command{
		{ID: "bad-image", Kind: "chat", Text: "check", Images: []llm.Image{{DataURL: "https://example.invalid/image"}}},
		{ID: "step-image", Kind: "continue", Images: []llm.Image{loopImage(t, 1)}},
	} {
		if _, err := l.Start("s1", cmd, nil); err == nil {
			t.Fatalf("accepted %+v", cmd)
		}
	}
	events, err := episodic.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type == episodic.RunState {
			t.Fatal("invalid image admitted run")
		}
	}
}

func TestPlanImagesFollowOnlyTheCurrentPlanningRequest(t *testing.T) {
	l, path := setupInterruptLoop(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected model call") })
	writer, err := l.open("s1")
	if err != nil {
		t.Fatal(err)
	}
	images, err := llm.ValidateImages([]llm.Image{loopImage(t, 20)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Append(episodic.MsgUser, UserMessage{Text: "first visual task", Images: images}); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Append(episodic.Plan, map[string]any{"title": "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Append(episodic.MsgUser, UserMessage{Text: "steering text after committed plan"}); err != nil {
		t.Fatal(err)
	}
	got, err := taskImages(path)
	if err != nil || len(got) != 1 || got[0].SHA256 != images[0].SHA256 {
		t.Fatalf("plan images %+v %v", got, err)
	}
	if _, err = writer.Append(episodic.MsgUser, UserMessage{Text: "unrelated next task"}); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Append(episodic.Plan, map[string]any{"title": "second"}); err != nil {
		t.Fatal(err)
	}
	got, err = taskImages(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("old task images leaked %+v %v", got, err)
	}
}
