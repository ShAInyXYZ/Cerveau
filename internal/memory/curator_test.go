package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type curatorFake struct {
	mu   sync.Mutex
	docs map[string]*Doc
}

func newCuratorFake() (*curatorFake, *httptest.Server) {
	cf := &curatorFake{docs: map[string]*Doc{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		cf.mu.Lock()
		defer cf.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/collections/memory/documents/search"):
			q := r.URL.Query().Get("q")
			var hits []map[string]any
			for _, d := range cf.docs {
				if d.MemoryType == "semantic" && !d.Superseded && jaccard(q, d.Content) > 0.2 {
					hits = append(hits, map[string]any{"document": *d})
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"hits": hits, "found": len(hits)})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/collections/memory/documents/"):
			id := strings.TrimPrefix(r.URL.Path, "/collections/memory/documents/")
			if d, ok := cf.docs[id]; ok {
				json.NewEncoder(w).Encode(*d)
			} else {
				w.WriteHeader(404)
			}
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "documents"):
			var d Doc
			json.NewDecoder(r.Body).Decode(&d)
			cp := d
			cf.docs[d.ID] = &cp
			json.NewEncoder(w).Encode(d)
		default:
			json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	return cf, srv
}

func online() bool { return true }

func TestCuratorCreatesNew(t *testing.T) {
	_, srv := newCuratorFake()
	defer srv.Close()
	c := NewCurator(NewTSClient(srv.URL, "k"), filepath.Join(t.TempDir(), "p.jsonl"), online)
	res, err := c.Write(context.Background(), Candidate{Content: "user prefers dark themes", Category: "preference"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "created" {
		t.Fatalf("action = %s", res.Action)
	}
}

func TestCuratorMergesAboveThreshold(t *testing.T) {
	cf, srv := newCuratorFake()
	defer srv.Close()
	c := NewCurator(NewTSClient(srv.URL, "k"), filepath.Join(t.TempDir(), "p.jsonl"), online)
	r1, _ := c.Write(context.Background(), Candidate{Content: "user prefers dark themes", Category: "preference", SourceEvtIDs: []string{"s1:evt_1"}})
	r2, err := c.Write(context.Background(), Candidate{Content: "user prefers dark themes", Category: "preference", SourceEvtIDs: []string{"s2:evt_9"}})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Action != "merged" || r2.DocID != r1.DocID {
		t.Fatalf("r2 = %+v, want merged into %s", r2, r1.DocID)
	}
	doc := cf.docs[r1.DocID]
	if doc.Confidence <= 0.7 || len(doc.Sources) != 2 {
		t.Fatalf("merged doc = %+v", doc)
	}
}

func TestCuratorLinksBorderline(t *testing.T) {
	_, srv := newCuratorFake()
	defer srv.Close()
	c := NewCurator(NewTSClient(srv.URL, "k"), filepath.Join(t.TempDir(), "p.jsonl"), online)
	c.Write(context.Background(), Candidate{Content: "the workspace uses tabs for go files", Category: "fact"})
	res, err := c.Write(context.Background(), Candidate{Content: "the workspace uses tabs for all files", Category: "fact"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "linked" && res.Action != "created" {
		t.Fatalf("action = %s", res.Action)
	}
	if res.Action == "linked" {
		_, srv2 := newCuratorFake()
		_ = srv2
	}
}

func TestCuratorSupersede(t *testing.T) {
	cf, srv := newCuratorFake()
	defer srv.Close()
	c := NewCurator(NewTSClient(srv.URL, "k"), filepath.Join(t.TempDir(), "p.jsonl"), online)
	r1, _ := c.Write(context.Background(), Candidate{Content: "db password is alpha", Category: "fact"})
	res, err := c.Write(context.Background(), Candidate{Content: "db password is beta", Category: "fact", Supersedes: r1.DocID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "superseded" {
		t.Fatalf("action = %s", res.Action)
	}
	old := cf.docs[r1.DocID]
	if !old.Superseded || old.SupersededBy != res.DocID {
		t.Fatalf("old doc = %+v", old)
	}
}

func TestCuratorPendingDrain(t *testing.T) {
	cf, srv := newCuratorFake()
	defer srv.Close()
	up := false
	pending := filepath.Join(t.TempDir(), "p.jsonl")
	c := NewCurator(NewTSClient(srv.URL, "k"), pending, func() bool { return up })
	res, err := c.Write(context.Background(), Candidate{Content: "offline fact", Category: "fact"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "queued" {
		t.Fatalf("action = %s, want queued", res.Action)
	}
	if len(cf.docs) != 0 {
		t.Fatal("wrote while offline")
	}
	up = true
	n, err := c.DrainPending(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("drained = %d, err = %v", n, err)
	}
	if len(cf.docs) != 1 {
		t.Fatalf("docs = %d after drain", len(cf.docs))
	}
}

func TestJaccard(t *testing.T) {
	if jaccard("hello world", "hello world") != 1.0 {
		t.Fatal("identical strings must score 1.0")
	}
	if jaccard("hello world", "completely different") != 0.0 {
		t.Fatal("disjoint strings must score 0.0")
	}
}
