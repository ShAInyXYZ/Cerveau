package api

import (
	"bufio"
	"cerveau/internal/episodic"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventStreamResumesAfterSnapshotCursor(t *testing.T) {
	for _, header := range []bool{false, true} {
		t.Run(map[bool]string{false: "query", true: "last-event-id"}[header], func(t *testing.T) {
			a, _, sid := setupAPI(t)
			wr, _ := a.Writer(sid)
			first, _ := wr.Append(episodic.Note, map[string]string{"text": "already in snapshot"})
			second, _ := wr.Append(episodic.Note, map[string]string{"text": "after snapshot"})
			mux := http.NewServeMux()
			mux.HandleFunc("GET /sessions/{id}/stream", a.StreamEvents)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			url := srv.URL + "/sessions/" + sid + "/stream"
			if !header {
				url += "?after=" + first.ID
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
			if header {
				req.Header.Set("Last-Event-ID", first.ID)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			scan := bufio.NewScanner(resp.Body)
			for scan.Scan() {
				line := scan.Text()
				if strings.HasPrefix(line, "id: ") {
					if line != "id: "+second.ID {
						t.Fatalf("replayed wrong cursor: %s", line)
					}
					return
				}
			}
			t.Fatalf("no resumed event: %v", scan.Err())
		})
	}
}
