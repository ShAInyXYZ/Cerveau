package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// StreamEvents is a Server-Sent Events endpoint that tails a session's
// events.jsonl: it replays whatever is already there, then follows the file,
// emitting each new appended event as it lands. This is how the UI shows the
// turn happening live (user msg -> tool calls -> results -> answer).
func (a *API) StreamEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	path := a.sess.EventsPath(id)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	f, err := os.Open(path)
	if err != nil {
		// session may not have its file yet; send a comment and keep the stream open
		fmt.Fprintf(w, ": waiting for %s\n\n", id)
		flusher.Flush()
		// try to open on a short delay
		for i := 0; i < 20 && err != nil; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			f, err = os.Open(path)
		}
		if err != nil {
			return
		}
	}
	defer f.Close()

	ctx := r.Context()
	beat := 0
	var pending []byte // bytes read past the last complete line

	sendLine := func(line []byte) {
		var probe map[string]any
		if json.Unmarshal(line, &probe) != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", line)
		flusher.Flush()
	}

	buf := make([]byte, 8192)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, rerr := f.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			// emit every complete newline-terminated line in pending
			for {
				i := indexByte(pending, '\n')
				if i < 0 {
					break
				}
				sendLine(pending[:i])
				pending = pending[i+1:]
			}
			continue
		}
		// no data available (EOF on the growing file) — idle-wait for appends
		_ = rerr
		select {
		case <-ctx.Done():
			return
		case <-time.After(150 * time.Millisecond):
		}
		beat++
		if beat%100 == 0 { // ~every 15s
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
