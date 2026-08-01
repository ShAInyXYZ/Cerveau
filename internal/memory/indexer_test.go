package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"cerveau/internal/episodic"
)

type captured struct {
	mu   sync.Mutex
	docs []Doc
}

func fakeTypesense(c *captured) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/collections" {
			w.WriteHeader(201)
			fmt.Fprint(w, `{"name":"memory"}`)
			return
		}
		if r.Method == http.MethodPost {
			var d Doc
			json.NewDecoder(r.Body).Decode(&d)
			c.mu.Lock()
			c.docs = append(c.docs, d)
			c.mu.Unlock()
			fmt.Fprint(w, `{"id":"`+d.ID+`"}`)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
}

func appendEvents(t *testing.T, sessDir, sid string, evs ...struct {
	typ episodic.EventType
	txt string
}) {
	t.Helper()
	wr, err := episodic.Open(filepath.Join(sessDir, sid, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if _, err := wr.Append(ev.typ, map[string]string{"text": ev.txt}); err != nil {
			t.Fatal(err)
		}
	}
	wr.Close()
}

func TestIndexerCursorAndCatchUp(t *testing.T) {
	c := &captured{}
	srv := fakeTypesense(c)
	defer srv.Close()

	sessDir := t.TempDir()
	cursorPath := filepath.Join(t.TempDir(), "cursor.json")
	appendEvents(t, sessDir, "s1",
		struct {
			typ episodic.EventType
			txt string
		}{episodic.MsgUser, "hello"},
		struct {
			typ episodic.EventType
			txt string
		}{episodic.MsgAssistant, "hi there"},
		struct {
			typ episodic.EventType
			txt string
		}{episodic.MsgUser, "how are you"},
	)

	ix := NewIndexer(NewTSClient(srv.URL, "k"), sessDir, cursorPath, "")
	ix.loadCursor()
	ix.Tick(context.Background())
	if len(c.docs) != 3 {
		t.Fatalf("first tick indexed %d docs, want 3", len(c.docs))
	}
	if c.docs[0].MemoryType != "episodic" || c.docs[0].SessionID != "s1" || c.docs[0].EvtID != "evt_000001" {
		t.Fatalf("doc = %+v", c.docs[0])
	}

	appendEvents(t, sessDir, "s1",
		struct {
			typ episodic.EventType
			txt string
		}{episodic.MsgAssistant, "fine"},
	)
	ix.Tick(context.Background())
	if len(c.docs) != 4 {
		t.Fatalf("second tick indexed %d docs, want 4", len(c.docs))
	}
	if c.docs[3].Content != "fine" {
		t.Fatalf("doc = %+v", c.docs[3])
	}

	ix2 := NewIndexer(NewTSClient(srv.URL, "k"), sessDir, cursorPath, "")
	ix2.loadCursor()
	ix2.Tick(context.Background())
	if len(c.docs) != 4 {
		t.Fatalf("catch-up re-indexed: %d docs, want 4", len(c.docs))
	}
}

func TestExtractContent(t *testing.T) {
	mk := func(payload string) episodic.Event {
		return episodic.Event{Payload: json.RawMessage(payload)}
	}
	if got := extractContent(mk(`{"text":"hello"}`)); got != "hello" {
		t.Fatalf("text: %q", got)
	}
	if got := extractContent(mk(`{"name":"read","output":"file body"}`)); got != "read: file body" {
		t.Fatalf("tool: %q", got)
	}
	if got := extractContent(mk(`{"detail":"boom"}`)); got != "boom" {
		t.Fatalf("detail: %q", got)
	}
	if got := extractContent(mk(`{}`)); got != "" {
		t.Fatalf("empty: %q", got)
	}
}
