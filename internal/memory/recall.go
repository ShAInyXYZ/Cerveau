package memory

import (
	"context"
	"fmt"
	"strings"

	"cerveau/internal/episodic"
)

const (
	PullMaxDocs   = 5
	PullDocChars  = 400
	pullTailScan  = 60
	pullQueryChars = 300
)

type Pull struct {
	DocID   string
	EvtID   string
	Content string
	Live    bool
}

type Recall struct {
	client  *TSClient
	sessDir string
	hybrid  bool
}

func NewRecall(client *TSClient, sessionsDir string, hybrid bool) *Recall {
	return &Recall{client: client, sessDir: sessionsDir, hybrid: hybrid}
}

func (r *Recall) TurnStart(ctx context.Context, sessionID, userMsg string, excludeEvtIDs map[string]bool) []Pull {
	return r.pull(ctx, sessionID, userMsg, excludeEvtIDs)
}

func (r *Recall) OnError(ctx context.Context, sessionID, errDetail string, excludeEvtIDs map[string]bool) []Pull {
	return r.pull(ctx, sessionID, errDetail, excludeEvtIDs)
}

func (r *Recall) pull(ctx context.Context, sessionID, query string, exclude map[string]bool) []Pull {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	if len(query) > pullQueryChars {
		query = query[:pullQueryChars]
	}
	pulls := []Pull{}
	seen := map[string]bool{}
	if r.client != nil {
		semHits, err := r.client.Search(ctx, query, "semantic", "", 2, r.hybrid, "superseded:=false")
		if err == nil {
			for _, h := range semHits {
				if len(pulls) >= PullMaxDocs {
					break
				}
				if seen[h.Doc.ID] {
					continue
				}
				seen[h.Doc.ID] = true
				pulls = append(pulls, Pull{DocID: h.Doc.ID, Content: "[semantic] " + trunc(h.Doc.Content, PullDocChars)})
			}
		}
		hits, err := r.client.Search(ctx, query, "episodic", "", PullMaxDocs*2, r.hybrid, "")
		if err == nil {
			for _, h := range hits {
				if len(pulls) >= PullMaxDocs {
					break
				}
				if seen[h.Doc.ID] || (h.Doc.SessionID == sessionID && exclude[h.Doc.EvtID]) {
					continue
				}
				seen[h.Doc.ID] = true
				pulls = append(pulls, Pull{DocID: h.Doc.ID, EvtID: h.Doc.EvtID, Content: trunc(h.Doc.Content, PullDocChars)})
			}
		}
	}
	pulls = append(pulls, r.liveTail(sessionID, query, exclude, seen, PullMaxDocs-len(pulls))...)
	return pulls
}

func (r *Recall) liveTail(sessionID, query string, exclude, seen map[string]bool, budget int) []Pull {
	if budget <= 0 || r.sessDir == "" {
		return nil
	}
	path := r.sessDir + "/" + sessionID + "/events.jsonl"
	events, err := episodic.Replay(path)
	if err != nil || len(events) == 0 {
		return nil
	}
	words := keywords(query)
	if len(words) == 0 {
		return nil
	}
	if len(events) > pullTailScan {
		events = events[len(events)-pullTailScan:]
	}
	var pulls []Pull
	for i := len(events) - 1; i >= 0 && len(pulls) < budget; i-- {
		ev := events[i]
		content := extractContent(ev)
		if content == "" || exclude[ev.ID] {
			continue
		}
		lower := strings.ToLower(content)
		for _, w := range words {
			if strings.Contains(lower, w) {
				docID := sessionID + ":" + ev.ID
				if !seen[docID] {
					seen[docID] = true
					pulls = append(pulls, Pull{DocID: docID, EvtID: ev.ID, Content: trunc(content, PullDocChars), Live: true})
				}
				break
			}
		}
	}
	return pulls
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true,
	"that": true, "from": true, "what": true, "how": true, "does": true,
	"are": true, "you": true, "your": true, "have": true, "has": true,
	"was": true, "were": true, "will": true, "would": true, "can": true,
	"could": true, "should": true, "about": true, "into": true, "when": true,
}

func keywords(query string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(query)) {
		w = strings.Trim(w, ".,;:!?()[]{}\"'")
		if len(w) > 3 && !stopwords[w] {
			out = append(out, w)
		}
	}
	return out
}

func FormatPulls(pulls []Pull) string {
	if len(pulls) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Recalled memory (auto, system-owned)\n")
	for _, p := range pulls {
		marker := ""
		if p.Live {
			marker = " (live tail)"
		}
		fmt.Fprintf(&sb, "- [%s%s] %s\n", p.EvtID, marker, strings.ReplaceAll(p.Content, "\n", " "))
	}
	return sb.String()
}
