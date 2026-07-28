package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cerveau/internal/episodic"
)

func searchServer(docs []Doc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		filter := r.URL.Query().Get("filter_by")
		memType := ""
		if strings.Contains(filter, "memory_type:=") {
			parts := strings.SplitN(strings.SplitN(filter, "memory_type:=", 2)[1], " ", 2)
			memType = strings.Trim(parts[0], "&")
		}
		var hits []map[string]any
		for _, d := range docs {
			if memType != "" && d.MemoryType != memType {
				continue
			}
			hits = append(hits, map[string]any{"document": d})
		}
		json.NewEncoder(w).Encode(map[string]any{"hits": hits, "found": len(hits)})
	}))
}

func TestTurnStartDedupAndCap(t *testing.T) {
	docs := []Doc{
		{ID: "s1:evt_000001", SessionID: "s1", MemoryType: "episodic", EvtID: "evt_000001", Content: "current session, in window"},
		{ID: "s0:evt_000001", SessionID: "s0", MemoryType: "episodic", EvtID: "evt_000001", Content: "foreign session, same evt id"},
	}
	for i := 2; i < 8; i++ {
		docs = append(docs, Doc{
			ID: fmt.Sprintf("s0:evt_%06d", i+1), SessionID: "s0", MemoryType: "episodic",
			EvtID: fmt.Sprintf("evt_%06d", i+1), Content: "some old fact",
		})
	}
	srv := searchServer(docs)
	defer srv.Close()
	r := NewRecall(NewTSClient(srv.URL, "k"), t.TempDir(), false)

	pulls := r.TurnStart(context.Background(), "s1", "tell me about facts", map[string]bool{"evt_000001": true})
	if len(pulls) != PullMaxDocs {
		t.Fatalf("pulls = %d, want %d (cap)", len(pulls), PullMaxDocs)
	}
	foreign := false
	for _, p := range pulls {
		if p.DocID == "s1:evt_000001" {
			t.Fatal("current-session windowed event was pulled")
		}
		if p.DocID == "s0:evt_000001" {
			foreign = true
		}
	}
	if !foreign {
		t.Fatal("foreign-session doc with same evt id must NOT be excluded")
	}
}

func TestLiveTailMergesUnindexed(t *testing.T) {
	srv := searchServer(nil)
	defer srv.Close()
	sessDir := t.TempDir()
	wr, err := episodic.Open(filepath.Join(sessDir, "s1", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	wr.Append(episodic.MsgUser, map[string]string{"text": "my database password rotated yesterday"})
	wr.Append(episodic.MsgAssistant, map[string]string{"text": "noted"})
	wr.Close()

	r := NewRecall(NewTSClient(srv.URL, "k"), sessDir, false)
	pulls := r.TurnStart(context.Background(), "s1", "what happened to the database password", nil)
	if len(pulls) != 1 || !pulls[0].Live {
		t.Fatalf("expected 1 live pull, got %+v", pulls)
	}
}

func TestKeywords(t *testing.T) {
	words := keywords("What is the database password for production?")
	for _, w := range words {
		if stopwords[w] || len(w) <= 3 {
			t.Fatalf("bad keyword %q in %v", w, words)
		}
	}
	if len(words) == 0 {
		t.Fatal("no keywords extracted")
	}
}
