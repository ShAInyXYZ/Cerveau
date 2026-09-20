package server

import (
	"net/http"

	"cerveau/internal/api"
	"cerveau/internal/panel"
)

func New(addr string, a *api.API) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.Health)
	mux.HandleFunc("GET /api/build", a.Build)
	mux.HandleFunc("GET /api/cores", a.ListCores)
	mux.HandleFunc("GET /api/sampling", a.GetSampling)
	mux.HandleFunc("POST /api/sampling", a.SetSampling)
	mux.HandleFunc("GET /api/thinking", a.GetThinking)
	mux.HandleFunc("POST /api/thinking", a.SetThinking)
	mux.HandleFunc("POST /api/cores/select", a.SelectCore)
	mux.HandleFunc("POST /api/cores/apply", a.ApplyCore)
	mux.HandleFunc("GET /api/cores/{id}/params", a.CoreParams)
	mux.HandleFunc("PUT /api/cores/{id}/params", a.SetCoreParams)
	mux.HandleFunc("GET /api/rig", a.Rig)
	mux.HandleFunc("POST /api/rig/plan", a.RigPlan)
	mux.HandleFunc("GET /api/rig/layouts", a.RigLayouts)
	mux.HandleFunc("PUT /api/rig/layouts", a.RigLayouts)
	mux.HandleFunc("DELETE /api/rig/layouts", a.RigLayouts)
	mux.HandleFunc("GET /api/idle", a.IdleStatus)
	mux.HandleFunc("POST /api/idle/stay", a.IdleStay)
	mux.HandleFunc("POST /api/idle/now", a.IdleNow)
	mux.HandleFunc("POST /api/idle/config", a.IdleConfig)
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
	// Step control: the one way to drive a committed plan. Both surfaces call
	// it — the native strip for "continue", the planner for per-step buttons —
	// so they cannot drift apart the way they did when the panel drove steps
	// by composing English prompts.
	mux.HandleFunc("GET /api/sessions/{id}/plan", a.PlanStateHandler)
	mux.HandleFunc("POST /api/sessions/{id}/plan/step", a.RunPlanStep)
	mux.HandleFunc("GET /api/sessions/{id}/usage", a.SessionUsage)
	mux.HandleFunc("POST /api/sessions/{id}/rewind", a.Rewind)
	mux.HandleFunc("POST /api/sessions/{id}/chat", a.Chat)
	mux.HandleFunc("POST /api/sessions/{id}/commands", a.Command)
	mux.HandleFunc("POST /api/sessions/{id}/images/devcheck", a.DevCheckImage)
	mux.HandleFunc("POST /api/sessions/{id}/resume", a.Resume)
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
	mux.HandleFunc("POST /api/files/probe", a.ProbeFiles)
	mux.HandleFunc("POST /api/config/workspace", a.ChangeWorkspace)
	mux.HandleFunc("POST /api/config/pick-workspace", a.PickWorkspace)
	// remote folder picking: the native dialog is useless from a phone
	mux.HandleFunc("GET /api/fs/list", a.FSList)
	mux.HandleFunc("POST /api/codegraph/index", a.ReindexCode)
	mux.HandleFunc("GET /api/system/stats", a.SystemStats)
	// Fleet management. authGate handles these for REMOTE callers behind the
	// full proof; registering them here is what makes them reachable from
	// loopback, where the gate short-circuits before that branch.
	registerDeviceRoutes(mux, a)
	mux.Handle("/", panel.Handler())
	return &http.Server{Addr: addr, Handler: authGate(a, mux)}
}
