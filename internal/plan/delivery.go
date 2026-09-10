package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"cerveau/internal/episodic"
)

// Plan writers share this admission lock. The run owner additionally excludes
// concurrent execution across processes; this lock covers in-process tool and
// state writers between journal snapshot and append.
var mutationLocks sync.Map

func LockMutation(sessionID string) func() {
	v, _ := mutationLocks.LoadOrStore(sessionID, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

type DeliveryMilestone struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type DeliveryContract struct {
	SourceEventID string              `json:"source_event_id,omitempty"`
	SourceSHA256  string              `json:"source_sha256"`
	Status        string              `json:"status"`
	Milestones    []DeliveryMilestone `json:"milestones,omitempty"`
}

// PlanningRequest deliberately reads user messages only, never tool output or
// model-supplied assertions about what the user requested. With beforePlan it
// resolves the request belonging to the latest committed plan.
func PlanningRequest(events []episodic.Event, beforePlan bool) (string, string) {
	end := len(events)
	if beforePlan {
		for i := end - 1; i >= 0; i-- {
			if events[i].Type == episodic.Plan {
				end = i
				break
			}
		}
	}
	for i := end - 1; i >= 0; i-- {
		if events[i].Type == episodic.MsgUser {
			var p struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(events[i].Payload, &p) == nil {
				return p.Text, events[i].ID
			}
		}
	}
	return "", ""
}

var stableIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var numberedMilestone = regexp.MustCompile(`^\s*([0-9]+)[.)]\s+(.+)$`)

func ValidStableID(id string) bool { return stableIDPattern.MatchString(id) }
func StepID(id string, index int) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("step-%d", index+1)
}

// ParseDelivery recognizes only explicit, named numbered lists, or an explicit
// JSON delivery_contract in the user's message. It never claims to understand
// ordering expressed in ordinary prose or infer semantic coverage of a check.
func ParseDelivery(text, eventID string) (*DeliveryContract, error) {
	sum := sha256.Sum256([]byte(text))
	c := &DeliveryContract{SourceEventID: eventID, SourceSHA256: hex.EncodeToString(sum[:]), Status: "not_semantically_validated"}
	var structured struct {
		Contract *struct {
			Milestones []DeliveryMilestone `json:"milestones"`
		} `json:"delivery_contract"`
	}
	if json.Unmarshal([]byte(text), &structured) == nil && structured.Contract != nil {
		c.Milestones = structured.Contract.Milestones
		c.Status = "explicit_order"
	} else {
		headings := 0
		for _, line := range strings.Split(text, "\n") {
			heading := strings.ToLower(strings.Trim(strings.TrimSpace(line), "#*: \t"))
			if heading == "delivery order" || heading == "delivery milestones" {
				headings++
			}
		}
		if headings > 1 {
			return nil, fmt.Errorf("multiple delivery lists require an explicit structured delivery_contract")
		}
		found, started := false, false
		for _, line := range strings.Split(text, "\n") {
			heading := strings.ToLower(strings.Trim(strings.TrimSpace(line), "#*: \t"))
			if heading == "delivery order" || heading == "delivery milestones" {
				if found {
					return nil, fmt.Errorf("multiple delivery lists require an explicit structured delivery_contract")
				}
				found = true
				continue
			}
			if !found {
				continue
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			m := numberedMilestone.FindStringSubmatch(line)
			if m == nil {
				if started && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
					c.Milestones[len(c.Milestones)-1].Text += "\n" + strings.TrimSpace(line)
					continue
				}
				break
			}
			started = true
			n, _ := strconv.Atoi(m[1])
			if n != len(c.Milestones)+1 {
				return nil, fmt.Errorf("delivery list must be consecutively numbered from 1")
			}
			c.Milestones = append(c.Milestones, DeliveryMilestone{ID: fmt.Sprintf("milestone-%d", n), Text: strings.TrimSpace(m[2])})
		}
		if found {
			c.Status = "explicit_order"
		}
	}
	if c.Status == "explicit_order" {
		if len(c.Milestones) == 0 || len(c.Milestones) > 64 {
			return nil, fmt.Errorf("explicit delivery contract needs 1 to 64 milestones")
		}
		seen := map[string]bool{}
		for _, m := range c.Milestones {
			if !ValidStableID(m.ID) || seen[m.ID] || strings.TrimSpace(m.Text) == "" || len(m.Text) > 8000 {
				return nil, fmt.Errorf("delivery milestones need unique stable IDs and nonempty bounded text")
			}
			seen[m.ID] = true
		}
	}
	return c, nil
}

// ValidateDelivery enforces membership, complete coverage and monotonic order.
// Labels are not proof that a check semantically covers the milestone: the
// exact user milestone must also remain visible in execution prompts.
func ValidateDelivery(c *DeliveryContract, stepMilestones []string) error {
	if c == nil || c.Status != "explicit_order" {
		for _, id := range stepMilestones {
			if id != "" {
				return fmt.Errorf("milestone_id has no explicit user delivery contract")
			}
		}
		return nil
	}
	positions := map[string]int{}
	for i, m := range c.Milestones {
		positions[m.ID] = i
	}
	last := -1
	for i, id := range stepMilestones {
		pos, ok := positions[id]
		if !ok {
			return fmt.Errorf("step %d needs a milestone_id from the explicit user delivery list", i+1)
		}
		if pos < last || pos > last+1 {
			return fmt.Errorf("step %d violates explicit delivery order (cannot skip or reorder milestones)", i+1)
		}
		last = pos
	}
	if last != len(c.Milestones)-1 {
		return fmt.Errorf("plan omits an explicit delivery milestone")
	}
	return nil
}
