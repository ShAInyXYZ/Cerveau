package memory

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"cerveau/internal/episodic"
)

func TestIndexerRetriesSchemaBeforeAdvancingAnyCursor(t *testing.T) {
	var ready atomic.Bool
	var schemas, upserts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/collections" {
			schemas.Add(1)
			if !ready.Load() {
				http.Error(w, "schema backend unavailable", 503)
				return
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"name":"memory"}`)
			return
		}
		upserts.Add(1)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	recoveryTestJournal(t, dir, "s1", []episodic.Event{
		recoveryTestEvent(1, episodic.Note, map[string]string{"kind": "reasoning"}),
		recoveryTestEvent(2, episodic.MsgUser, map[string]string{"text": "must survive the outage"}),
	})
	cursor := filepath.Join(t.TempDir(), "cursor.json")
	ix := NewIndexer(NewTSClient(srv.URL, "k"), dir, cursor, "")
	now := time.Unix(100, 0)
	ix.now = func() time.Time { return now }
	for i, wantDelay := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second} {
		ix.Tick(context.Background())
		if got := schemas.Load(); got != int32(i+1) {
			t.Fatalf("schema attempts=%d want=%d", got, i+1)
		}
		if ix.schemaBackoff != wantDelay || ix.schemaReady {
			t.Fatalf("retry state: ready=%v backoff=%v", ix.schemaReady, ix.schemaBackoff)
		}
		if len(ix.cursor) != 0 || upserts.Load() != 0 {
			t.Fatalf("advanced before readiness: cursor=%v upserts=%d", ix.cursor, upserts.Load())
		}
		if _, err := os.Stat(cursor); !os.IsNotExist(err) {
			t.Fatalf("cursor touched while schema unavailable: %v", err)
		}
		for j := 0; j < 4; j++ {
			ix.Tick(context.Background())
		}
		if got := schemas.Load(); got != int32(i+1) {
			t.Fatalf("ignored retry backoff: %d", got)
		}
		now = now.Add(wantDelay)
	}
	ready.Store(true)
	ix.Tick(context.Background())
	if !ix.schemaReady || ix.schemaBackoff != 0 || !ix.schemaRetryAt.IsZero() || ix.cursor["s1"] != "evt_000002" || upserts.Load() != 1 {
		t.Fatalf("failed recovery: ready=%v backoff=%v cursor=%v upserts=%d", ix.schemaReady, ix.schemaBackoff, ix.cursor, upserts.Load())
	}
	ix.Tick(context.Background())
	if schemas.Load() != 7 || upserts.Load() != 1 {
		t.Fatalf("repeated healthy work: schemas=%d upserts=%d", schemas.Load(), upserts.Load())
	}
}

func TestIndexerRechecksSchemaAfterUpsertOutage(t *testing.T) {
	var failWrites atomic.Bool
	var schemas, writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/collections" {
			schemas.Add(1)
			w.WriteHeader(201)
			fmt.Fprint(w, `{"name":"memory"}`)
			return
		}
		writes.Add(1)
		if failWrites.Load() {
			http.Error(w, "collection missing after restart", 404)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	events := []episodic.Event{recoveryTestEvent(1, episodic.MsgUser, map[string]string{"text": "first"})}
	recoveryTestJournal(t, dir, "s1", events)
	ix := NewIndexer(NewTSClient(srv.URL, "k"), dir, filepath.Join(t.TempDir(), "cursor.json"), "")
	now := time.Unix(100, 0)
	ix.now = func() time.Time { return now }
	ix.Tick(context.Background())
	events = append(events, recoveryTestEvent(2, episodic.MsgUser, map[string]string{"text": "second"}))
	recoveryTestJournal(t, dir, "s1", events)
	failWrites.Store(true)
	ix.Tick(context.Background())
	if ix.schemaReady || ix.cursor["s1"] != "evt_000001" {
		t.Fatalf("write outage advanced cursor: %v ready=%v", ix.cursor, ix.schemaReady)
	}
	ix.Tick(context.Background())
	if schemas.Load() != 1 || writes.Load() != 2 {
		t.Fatalf("outage retried without backoff: schemas=%d writes=%d", schemas.Load(), writes.Load())
	}
	now = now.Add(schemaRetryInitial)
	failWrites.Store(false)
	ix.Tick(context.Background())
	if schemas.Load() != 2 || writes.Load() != 3 || ix.cursor["s1"] != "evt_000002" {
		t.Fatalf("did not recover through schema: schemas=%d writes=%d cursor=%v", schemas.Load(), writes.Load(), ix.cursor)
	}
}

func TestIndexerSchemaAttemptHasBoundedTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer srv.Close()
	ix := NewIndexer(NewTSClient(srv.URL, "k"), t.TempDir(), filepath.Join(t.TempDir(), "cursor.json"), "")
	start := time.Now()
	ix.Tick(context.Background())
	if elapsed := time.Since(start); elapsed > schemaAttemptTimeout+time.Second {
		t.Fatalf("unbounded schema attempt: %v", elapsed)
	}
	if ix.schemaReady || ix.schemaBackoff != schemaRetryInitial || len(ix.cursor) != 0 {
		t.Fatalf("timeout did not gate indexing: ready=%v backoff=%v cursor=%v", ix.schemaReady, ix.schemaBackoff, ix.cursor)
	}
}
