package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"cerveau/internal/episodic"
	"cerveau/internal/memory"
)

func (a *API) SetMemory(c *memory.TSClient) { a.mem = c }

func (a *API) MemorySearch(w http.ResponseWriter, r *http.Request) {
	if a.mem == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory offline (T0)"})
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q required"})
		return
	}
	memType := r.URL.Query().Get("memory_type")
	hits, err := a.mem.Search(r.Context(), q, memType, "", 20, false, "")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	docs := []memory.Doc{}
	for _, h := range hits {
		docs = append(docs, h.Doc)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": docs})
}

// MemoryList lists memories across ALL types (or filtered by ?type= and
// ?session=), so the browser can show more than just semantic hubs. Uses a
// wildcard query; superseded semantic docs are hidden.
func (a *API) MemoryList(w http.ResponseWriter, r *http.Request) {
	if a.mem == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory offline (T0)"})
		return
	}
	memType := r.URL.Query().Get("type")      // "", "semantic", "episodic", …
	sessionID := r.URL.Query().Get("session") // "" = all sessions
	// hide superseded semantic docs; harmless for episodic (field defaults false)
	hits, err := a.mem.Search(r.Context(), "*", memType, sessionID, 200, false, "superseded:=false")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	docs := []memory.Doc{}
	for _, h := range hits {
		docs = append(docs, h.Doc)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": docs})
}

func (a *API) MemoryReview(w http.ResponseWriter, r *http.Request) {
	if a.mem == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory offline (T0)"})
		return
	}
	hits, err := a.mem.Search(r.Context(), "*", "semantic", "", 50, false, "review:=true")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	docs := []memory.Doc{}
	for _, h := range hits {
		docs = append(docs, h.Doc)
	}
	writeJSON(w, http.StatusOK, map[string]any{"review": docs})
}

func (a *API) MemoryReviewResolve(w http.ResponseWriter, r *http.Request) {
	if a.mem == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory offline (T0)"})
		return
	}
	id := r.PathValue("id")
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action required"})
		return
	}
	doc, err := a.mem.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "doc not found"})
		return
	}
	switch body.Action {
	case "keep":
		doc.Review = false
	case "supersede":
		doc.Review = false
		doc.Superseded = true
		if len(doc.RelatedTo) > 0 {
			doc.SupersededBy = doc.RelatedTo[0]
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be keep or supersede"})
		return
	}
	if err := a.mem.Upsert(r.Context(), *doc); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (a *API) MemoryProvenance(w http.ResponseWriter, r *http.Request) {
	if a.mem == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory offline (T0)"})
		return
	}
	id := r.PathValue("id")
	doc, err := a.mem.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "doc not found"})
		return
	}
	type provenanceEvent struct {
		Session string         `json:"session"`
		Event   episodic.Event `json:"event"`
	}
	found := []provenanceEvent{}
	for _, src := range doc.Sources {
		parts := strings.SplitN(src, ":", 2)
		sessionID := parts[0]
		if sessionID == "" {
			continue
		}
		events, err := episodic.Replay(a.sess.EventsPath(sessionID))
		if err != nil {
			continue
		}
		if len(parts) == 1 {
			continue
		}
		evtID := parts[1]
		for _, ev := range events {
			if ev.ID == evtID {
				found = append(found, provenanceEvent{Session: sessionID, Event: ev})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"doc": doc, "events": found})
}

// MemoryGraph returns semantic facts as hub nodes plus the docs they link to
// (related_to, superseded_by) and their source episodic docs — everything the
// spider-web needs. Nodes carry relationships so the client draws the edges.
func (a *API) MemoryGraph(w http.ResponseWriter, r *http.Request) {
	if a.mem == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory offline (T0)"})
		return
	}
	// primary hubs: semantic facts (not superseded)
	sem, err := a.mem.Search(r.Context(), "*", "semantic", "", 60, false, "superseded:=false")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	seen := map[string]bool{}
	nodes := []memory.Doc{}
	add := func(d memory.Doc) {
		if d.ID == "" || seen[d.ID] {
			return
		}
		seen[d.ID] = true
		nodes = append(nodes, d)
	}
	for _, h := range sem {
		add(h.Doc)
	}
	// resolve linked docs (related_to, superseded_by) so the client can draw edges to real nodes
	for _, h := range sem {
		for _, rid := range append(append([]string{}, h.Doc.RelatedTo...), h.Doc.SupersededBy) {
			if rid == "" || seen[rid] {
				continue
			}
			if d, err := a.mem.Get(r.Context(), rid); err == nil && d != nil {
				add(*d)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}
