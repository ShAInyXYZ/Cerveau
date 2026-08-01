// rfx_distill.go — crvcli rfx distill: convert a legacy prose skill
// (~/.crv/skills/*.md) into a draft .rfx.yaml via the running server's model.
//
// HUMAN-APPROVED BY DESIGN (spec §9): the draft is printed for review and
// NEVER auto-installed. The path is: distill → read it → crvcli rfx install.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cerveau/internal/rfx"
)

const distillPrompt = `Convert this legacy skill file into an RFX v1 reflex manifest (YAML).

Rules for the output — follow exactly:
- Output ONLY the YAML, no commentary, no code fences.
- Start with "rfx: 1". Fields: name ([a-z0-9-]), description (one sentence, ≤200 chars), kind (pipeline|exec), risk (safe|sensitive|dangerous — REQUIRED, no default), modes, ingress_cap, card, params (JSON Schema), steps.
- kind pipeline: steps is a list of single-key maps of tool name to args. Tools available: bash, read, write, edit, grep, web_fetch.
- Placeholders: {{ params.NAME }} and {{ steps.ID.output }}. In bash strings, embedded placeholders are auto-quoted — do not add your own quotes around them.
- A pipeline containing a bash step may NOT declare risk: safe.
- Prose instructions and "how to think" content do NOT belong in the reflex — drop them. The reflex is wiring, not documentation.
- If the skill declares tools with command templates, each becomes one reflex — output only the FIRST one.

The skill file follows:
---
%s`

func (c *client) rfxDistill(target string) error {
	path := target
	if !strings.HasSuffix(path, ".md") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".crv", "skills", target+".md")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// One-shot session, discussion mode: this is a mechanical transform,
	// not research.
	m, err := c.post("/api/sessions", map[string]string{"name": "rfx distill: " + filepath.Base(path)})
	if err != nil {
		return fmt.Errorf("distill needs a running crv server (model-assisted): %w", err)
	}
	session := str(m["id"])
	res, err := c.post("/api/sessions/"+session+"/chat", map[string]string{
		"text": fmt.Sprintf(distillPrompt, string(data)),
		"mode": "discussion",
	})
	if err != nil {
		return err
	}
	reply := str(res["reply"])

	yamlText, err := extractManifest(reply)
	if err != nil {
		return fmt.Errorf("model reply contained no usable manifest: %w\n--- reply ---\n%s", err, reply)
	}

	// Validate the draft locally BEFORE showing it — a distill that doesn't
	// pass the spec is shown as a failure with the reason, not shipped.
	r, perr := rfx.Parse([]byte(yamlText), "draft.rfx.yaml")
	if perr == nil {
		r.Path = r.Name + ".rfx.yaml" // neutralize stem rule for the draft
		perr = rfx.Validate(r, knownCoreTool)
	}

	fmt.Println("--- draft " + r.Name + ".rfx.yaml (REVIEW BEFORE INSTALL — distill is human-approved, spec §9) ---")
	fmt.Println(yamlText)
	fmt.Println("---")
	if perr != nil {
		return fmt.Errorf("draft FAILED validation: %v — fix the draft or re-distill", perr)
	}
	fmt.Printf("draft validates clean. To adopt: save it as %s.rfx.yaml, then: crvcli rfx install %s.rfx.yaml\n", r.Name, r.Name)
	return nil
}

// extractManifest pulls the YAML out of a model reply: fenced block if
// present, else from the first "rfx: 1" line onward.
func extractManifest(reply string) (string, error) {
	if i := strings.Index(reply, "```"); i >= 0 {
		rest := reply[i+3:]
		rest = strings.TrimPrefix(rest, "yaml")
		rest = strings.TrimPrefix(rest, "yml")
		if j := strings.Index(rest, "```"); j > 0 {
			rest = rest[:j]
		}
		if strings.Contains(rest, "rfx:") {
			return strings.TrimSpace(rest), nil
		}
	}
	if i := strings.Index(reply, "rfx: 1"); i >= 0 {
		return strings.TrimSpace(reply[i:]), nil
	}
	return "", fmt.Errorf("no rfx manifest found")
}
