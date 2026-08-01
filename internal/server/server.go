package server

import (
	"net/http"

	"cerveau/internal/api"
	"cerveau/internal/panel"
)

func New(addr string, a *api.API) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.Health)
	mux.HandleFunc("GET /api/sessions", a.ListSessions)
	mux.HandleFunc("POST /api/sessions", a.CreateSession)
	mux.HandleFunc("POST /api/sessions/instant", a.CreateInstant)
	mux.HandleFunc("PATCH /api/sessions/{id}", a.RenameSession)
	mux.HandleFunc("GET /api/sessions/{id}/delete-preview", a.DeletePreview)
	mux.HandleFunc("DELETE /api/sessions/{id}", a.DeleteSession)
	mux.HandleFunc("GET /api/sessions/{id}/events", a.SessionEvents)
	mux.HandleFunc("GET /api/sessions/{id}/stream", a.StreamEvents)
	mux.HandleFunc("POST /api/sessions/{id}/events", a.AppendEvent)
	mux.HandleFunc("GET /api/sessions/{id}/state", a.SessionState)
	mux.HandleFunc("GET /api/sessions/{id}/errors", a.SessionErrors)
	mux.HandleFunc("GET /api/sessions/{id}/report", a.SessionReport)
	mux.HandleFunc("POST /api/sessions/{id}/chat", a.Chat)
	mux.HandleFunc("POST /api/sessions/{id}/autopilot", a.Autopilot)
	mux.HandleFunc("POST /api/sessions/{id}/steer", a.Steer)
	mux.HandleFunc("POST /api/sessions/{id}/pause", a.Pause)
	mux.HandleFunc("POST /api/sessions/{id}/kill", a.Kill)
	mux.HandleFunc("GET /api/sessions/{id}/question", a.PendingQuestion)
	mux.HandleFunc("POST /api/sessions/{id}/answer", a.Answer)
	mux.HandleFunc("GET /api/memory/search", a.MemorySearch)
	mux.HandleFunc("GET /api/memory/list", a.MemoryList)
	mux.HandleFunc("GET /api/memory/graph", a.MemoryGraph)
	mux.HandleFunc("GET /api/memory/review", a.MemoryReview)
	mux.HandleFunc("POST /api/memory/review/{id}", a.MemoryReviewResolve)
	mux.HandleFunc("GET /api/memory/provenance/{id}", a.MemoryProvenance)
	mux.HandleFunc("GET /api/skills", a.ListSkills)
	mux.HandleFunc("GET /api/rfx", a.ListRfx)
	mux.HandleFunc("POST /api/rfx/toggle", a.ToggleRfx)
	mux.HandleFunc("POST /api/rfx/run", a.RunRfx)
	mux.HandleFunc("GET /api/rfx/panel/{pack}", a.PanelRfx)
	mux.HandleFunc("POST /api/config/workspace", a.ChangeWorkspace)
	mux.HandleFunc("POST /api/config/pick-workspace", a.PickWorkspace)
	mux.HandleFunc("POST /api/codegraph/index", a.ReindexCode)
	mux.HandleFunc("GET /api/system/stats", a.SystemStats)
	mux.Handle("/", panel.Handler())
	return &http.Server{Addr: addr, Handler: mux}
}
