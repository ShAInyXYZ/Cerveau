package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"cerveau/internal/config"
	"cerveau/internal/episodic"
	"cerveau/internal/memory"
	"cerveau/internal/session"
)

type memFake struct {
	mu   sync.Mutex
	docs map[string]*memory.Doc
}

func newMemFake() (*memFake, *httptest.Server) {
	mf := &memFake{docs: map[string]*memory.Doc{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		mf.mu.Lock()
		defer mf.mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "search"):
			var hits []map[string]any
			for _, d := range mf.docs {
				if strings.Contains(r.URL.RawQuery, "review:=true") && !d.Review {
					continue
				}
				hits = append(hits, map[string]any{"document": *d})
			}
			json.NewEncoder(w).Encode(map[string]any{"hits": hits, "found": len(hits)})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "documents/"):
			id := strings.TrimPrefix(r.URL.Path, "/collections/memory/documents/")
			if d, ok := mf.docs[id]; ok {
				json.NewEncoder(w).Encode(*d)
			} else {
				w.WriteHeader(404)
			}
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "documents"):
			var d memory.Doc
			json.NewDecoder(r.Body).Decode(&d)
			cp := d
			mf.docs[d.ID] = &cp
			json.NewEncoder(w).Encode(d)
		default:
			json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	return mf, srv
}

func setupAPI(t *testing.T) (*API, *memFake, string) {
	t.Helper()
	mf, srv := newMemFake()
	t.Cleanup(srv.Close)
	cfg := config.Default()
	cfg.SessionsDir = t.TempDir()
	sess, _ := session.NewFSStore(cfg.SessionsDir)
	a := New(cfg, sess)
	a.SetMemory(memory.NewTSClient(srv.URL, "k"))
	m, _ := sess.Create("prov test")
	return a, mf, m.ID
}

func TestReviewQueueAndResolve(t *testing.T) {
	a, mf, _ := setupAPI(t)
	mf.docs["sem_1"] = &memory.Doc{ID: "sem_1", MemoryType: "semantic", Content: "fact one", Review: true, RelatedTo: []string{"sem_0"}}

	req := httptest.NewRequest(http.MethodGet, "/api/memory/review", nil)
	rec := httptest.NewRecorder()
	a.MemoryReview(rec, req)
	var out struct {
		Review []memory.Doc `json:"review"`
	}
	json.NewDecoder(rec.Body).Decode(&out)
	if len(out.Review) != 1 {
		t.Fatalf("review = %+v", out.Review)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/memory/review/sem_1",
		strings.NewReader(`{"action":"supersede"}`))
	req.SetPathValue("id", "sem_1")
	rec = httptest.NewRecorder()
	a.MemoryReviewResolve(rec, req)
	doc := mf.docs["sem_1"]
	if doc.SupersededBy != "sem_0" || !doc.Superseded || doc.Review {
		t.Fatalf("doc = %+v", doc)
	}
}

func TestProvenance(t *testing.T) {
	a, mf, sessID := setupAPI(t)
	wr, _ := episodic.Open(a.sess.EventsPath(sessID))
	ev, _ := wr.Append(episodic.MsgUser, map[string]string{"text": "the source message"})
	wr.Close()
	mf.docs["sem_9"] = &memory.Doc{
		ID: "sem_9", MemoryType: "semantic", Content: "derived fact",
		Sources: []string{sessID + ":" + ev.ID},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/memory/provenance/sem_9", nil)
	req.SetPathValue("id", "sem_9")
	rec := httptest.NewRecorder()
	a.MemoryProvenance(rec, req)
	var out struct {
		Events []struct {
			Event episodic.Event `json:"event"`
		} `json:"events"`
	}
	json.NewDecoder(rec.Body).Decode(&out)
	if len(out.Events) != 1 || out.Events[0].Event.ID != ev.ID {
		t.Fatalf("events = %+v", out.Events)
	}
}
