package api

import (
	"encoding/json"
	"net/http"

	"cerveau/internal/episodic"
)

// SessionUsage is what a session has cost so far.
//
// Nothing recorded token counts until now, so "which Core reaches a working
// answer for fewer tokens" — the question a whole day of benchmarking was
// trying to answer — could not be read off the log at all. It can now.
type SessionUsage struct {
	Calls             int `json:"calls"`
	PromptTokens      int `json:"prompt_tokens"`
	CompletionTokens  int `json:"completion_tokens"`
	CachedTokens      int `json:"cached_tokens"`
	FreshPromptTokens int `json:"fresh_prompt_tokens"`

	// CacheHitRate is the share of prompt tokens served from cache.
	//
	// 0 is ambiguous by construction: the local vLLM build reports
	// prompt_tokens_details: null, so a working cache and an absent cache look
	// identical here. A UI showing this must say "not reported" rather than
	// "0%" when cached_tokens is 0 — claiming a measured miss would be worse
	// than admitting no data.
	CacheHitRate float64 `json:"cache_hit_rate"`
}

// sumUsage folds every assistant turn's usage into one total.
//
// A log written before usage existed simply contributes nothing — absent is
// distinguishable from zero, and an old session reports no data rather than a
// confident 0 tokens.
func sumUsage(events []episodic.Event) SessionUsage {
	var out SessionUsage
	for _, ev := range events {
		if ev.Type != episodic.MsgAssistant {
			continue
		}
		var p struct {
			Usage *struct {
				PromptTokens      int `json:"prompt_tokens"`
				CompletionTokens  int `json:"completion_tokens"`
				CachedTokens      int `json:"cached_tokens"`
				FreshPromptTokens int `json:"fresh_prompt_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil || p.Usage == nil {
			continue
		}
		out.Calls++
		out.PromptTokens += p.Usage.PromptTokens
		out.CompletionTokens += p.Usage.CompletionTokens
		out.CachedTokens += p.Usage.CachedTokens
		if p.Usage.FreshPromptTokens > 0 {
			out.FreshPromptTokens += p.Usage.FreshPromptTokens
		} else {
			// older payloads recorded no cached split
			out.FreshPromptTokens += p.Usage.PromptTokens - p.Usage.CachedTokens
		}
	}
	if out.PromptTokens > 0 {
		out.CacheHitRate = float64(out.CachedTokens) / float64(out.PromptTokens)
	}
	return out
}

// GET /api/sessions/{id}/usage
func (a *API) SessionUsage(w http.ResponseWriter, r *http.Request) {
	events, err := episodic.Replay(a.sess.EventsPath(r.PathValue("id")))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sumUsage(events))
}
